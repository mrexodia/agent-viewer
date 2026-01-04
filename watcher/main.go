package main

import (
	"bufio"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/gorilla/websocket"
)

// LineMessage is the message format to send to server
type LineMessage struct {
	Type string `json:"type"` // Always "line"
	Path string `json:"path"` // Relative path
	Line string `json:"line"` // Raw JSONL line content (includes \n)
}

// FileState tracks the read position of a file
type FileState struct {
	Path     string // Relative path from watch root
	LastLine int    // Last line number sent
	LastSize int64  // Last known file size
}

// Watcher monitors a directory and sends updates to server
type Watcher struct {
	watchDir  string
	serverURL string
	batchMs   int
	conn      *websocket.Conn
	connMu    sync.Mutex
	files     map[string]*FileState
	filesMu   sync.RWMutex
	lineQueue chan LineMessage
	done      chan struct{}
	fsWatcher *fsnotify.Watcher
}

// NewWatcher creates a new watcher instance
func NewWatcher(watchDir, serverURL string, batchMs int) *Watcher {
	return &Watcher{
		watchDir:  watchDir,
		serverURL: serverURL,
		batchMs:   batchMs,
		files:     make(map[string]*FileState),
		lineQueue: make(chan LineMessage, 10000),
		done:      make(chan struct{}),
	}
}

// Connect establishes WebSocket connection to server
func (w *Watcher) Connect() error {
	w.connMu.Lock()
	defer w.connMu.Unlock()

	conn, _, err := websocket.DefaultDialer.Dial(w.serverURL, nil)
	if err != nil {
		return err
	}
	w.conn = conn
	log.Printf("Connected to server: %s", w.serverURL)
	return nil
}

// Reconnect attempts to reconnect with exponential backoff
func (w *Watcher) Reconnect() {
	backoff := time.Second
	maxBackoff := 30 * time.Second

	for {
		select {
		case <-w.done:
			return
		default:
		}

		log.Printf("Attempting to reconnect in %v...", backoff)
		time.Sleep(backoff)

		if err := w.Connect(); err != nil {
			log.Printf("Reconnection failed: %v", err)
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
		} else {
			log.Printf("Reconnected successfully")
			return
		}
	}
}

// IsConnected checks if we have an active connection
func (w *Watcher) IsConnected() bool {
	w.connMu.Lock()
	defer w.connMu.Unlock()
	return w.conn != nil
}

// ErrNotConnected is returned when trying to send while disconnected
var ErrNotConnected = fmt.Errorf("not connected to server")

// sendLine sends a single line message (internal, must hold connMu)
func (w *Watcher) sendLine(msg LineMessage) error {
	w.connMu.Lock()
	defer w.connMu.Unlock()

	if w.conn == nil {
		return ErrNotConnected
	}
	return w.conn.WriteJSON(msg)
}

// batchSender collects lines and sends them in batches
func (w *Watcher) batchSender() {
	ticker := time.NewTicker(time.Duration(w.batchMs) * time.Millisecond)
	defer ticker.Stop()

	var batch []LineMessage

	for {
		select {
		case msg := <-w.lineQueue:
			batch = append(batch, msg)
		case <-ticker.C:
			if len(batch) > 0 {
				failed := false
				for _, msg := range batch {
					if err := w.sendLine(msg); err != nil {
						if err != ErrNotConnected {
							log.Printf("Error sending line: %v", err)
						}
						failed = true
						break
					}
				}
				if failed {
					// Connection lost, close and reconnect
					w.connMu.Lock()
					if w.conn != nil {
						w.conn.Close()
						w.conn = nil
					}
					w.connMu.Unlock()

					go w.Reconnect()
					// Keep batch for retry after reconnect
				} else {
					batch = batch[:0]
				}
			}
		case <-w.done:
			// Send remaining batch before exit
			for _, msg := range batch {
				w.sendLine(msg)
			}
			return
		}
	}
}

// normalizePath converts OS-specific path separators to forward slashes
// for consistent cross-platform path handling
func normalizePath(path string) string {
	return strings.ReplaceAll(path, "\\", "/")
}

// scanDirectory finds all .jsonl files and reads them
func (w *Watcher) scanDirectory() error {
	return filepath.Walk(w.watchDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			log.Printf("Error accessing path %s: %v", path, err)
			return nil // Continue walking
		}

		if info.IsDir() {
			return nil
		}

		if !strings.HasSuffix(path, ".jsonl") {
			return nil
		}

		relPath, err := filepath.Rel(w.watchDir, path)
		if err != nil {
			log.Printf("Error getting relative path for %s: %v", path, err)
			return nil
		}
		// Normalize to forward slashes for cross-platform consistency
		relPath = normalizePath(relPath)

		if err := w.readFile(path, relPath); err != nil {
			log.Printf("Error reading file %s: %v", path, err)
		}

		return nil
	})
}

// readFile reads all lines from a file and queues them
func (w *Watcher) readFile(absPath, relPath string) error {
	file, err := os.Open(absPath)
	if err != nil {
		return err
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return err
	}

	w.filesMu.Lock()
	state, exists := w.files[relPath]
	if !exists {
		state = &FileState{
			Path:     relPath,
			LastLine: 0,
			LastSize: 0,
		}
		w.files[relPath] = state
	}

	// Handle file truncation - reset and resend all
	if info.Size() < state.LastSize {
		log.Printf("File %s was truncated, resending all lines", relPath)
		state.LastLine = 0
		state.LastSize = 0
	}
	w.filesMu.Unlock()

	// Use bufio.Reader for unlimited line length support
	// bufio.Scanner has a max token size, but ReadString('\n') grows dynamically
	reader := bufio.NewReader(file)
	
	lineNum := 0
	newLines := 0
	for {
		line, err := reader.ReadString('\n')
		if err != nil && err.Error() != "EOF" && line == "" {
			return err
		}
		if line == "" {
			break // EOF with no more data
		}
		
		lineNum++
		w.filesMu.RLock()
		lastLine := state.LastLine
		w.filesMu.RUnlock()

		if lineNum <= lastLine {
			if err != nil {
				break // EOF
			}
			continue // Skip already sent lines
		}

		// Ensure line ends with newline (last line might not)
		if !strings.HasSuffix(line, "\n") {
			line = line + "\n"
		}
		
		w.lineQueue <- LineMessage{
			Type: "line",
			Path: relPath,
			Line: line,
		}
		newLines++
		
		if err != nil {
			break // EOF after processing last line
		}
	}

	// Update state
	w.filesMu.Lock()
	state.LastLine = lineNum
	state.LastSize = info.Size()
	w.filesMu.Unlock()

	if newLines > 0 {
		log.Printf("Read %d new lines from %s (total: %d)", newLines, relPath, lineNum)
	}
	return nil
}

// readNewLines reads only new lines from a modified file
func (w *Watcher) readNewLines(absPath, relPath string) error {
	return w.readFile(absPath, relPath)
}

// setupFSWatcher creates and configures the fsnotify watcher
func (w *Watcher) setupFSWatcher() error {
	fsWatcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	w.fsWatcher = fsWatcher

	// Add watch directory and all subdirectories
	err = filepath.Walk(w.watchDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // Skip errors
		}
		if info.IsDir() {
			if err := fsWatcher.Add(path); err != nil {
				log.Printf("Warning: could not watch %s: %v", path, err)
			}
		}
		return nil
	})

	return err
}

// addDirectoryRecursive adds a directory and all its subdirectories to the watcher
// and scans for any existing .jsonl files
func (w *Watcher) addDirectoryRecursive(dir string) {
	filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // Skip errors
		}

		if info.IsDir() {
			if err := w.fsWatcher.Add(path); err != nil {
				log.Printf("Warning: could not watch %s: %v", path, err)
			} else {
				log.Printf("Now watching directory: %s", path)
			}
		} else if strings.HasSuffix(path, ".jsonl") {
			// Found a .jsonl file in the new directory tree - read it
			relPath, err := filepath.Rel(w.watchDir, path)
			if err != nil {
				log.Printf("Error getting relative path for %s: %v", path, err)
				return nil
			}
			// Normalize to forward slashes for cross-platform consistency
			relPath = normalizePath(relPath)
			if err := w.readFile(path, relPath); err != nil {
				log.Printf("Error reading %s: %v", path, err)
			}
		}
		return nil
	})
}

// handleFSEvents processes filesystem events
func (w *Watcher) handleFSEvents() {
	for {
		select {
		case event, ok := <-w.fsWatcher.Events:
			if !ok {
				return
			}

			// Handle directory events first (for any path)
			if event.Op&fsnotify.Create != 0 {
				info, err := os.Stat(event.Name)
				if err == nil && info.IsDir() {
					// Recursively add directory and scan for .jsonl files
					w.addDirectoryRecursive(event.Name)
					continue
				}
			}

			// Only care about .jsonl files from here on
			if !strings.HasSuffix(event.Name, ".jsonl") {
				continue
			}

			// Handle Write, Create, and Rename events for .jsonl files
			// Rename is important for atomic writes (temp file -> final name)
			// Chmod can also indicate file availability on some systems
			if event.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename|fsnotify.Chmod) != 0 {
				// For Rename, check if the file now exists (it was renamed TO this name)
				if event.Op&fsnotify.Rename != 0 {
					if _, err := os.Stat(event.Name); err != nil {
						// File was renamed away, not to this name
						continue
					}
				}

				relPath, err := filepath.Rel(w.watchDir, event.Name)
				if err != nil {
					log.Printf("Error getting relative path: %v", err)
					continue
				}
				// Normalize to forward slashes for cross-platform consistency
				relPath = normalizePath(relPath)

				if err := w.readNewLines(event.Name, relPath); err != nil {
					log.Printf("Error reading %s: %v", event.Name, err)
				}
			}

		case err, ok := <-w.fsWatcher.Errors:
			if !ok {
				return
			}
			log.Printf("fsnotify error: %v", err)

		case <-w.done:
			return
		}
	}
}

// Run starts the watcher
func (w *Watcher) Run() error {
	// Check watch directory exists
	info, err := os.Stat(w.watchDir)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return os.ErrNotExist
	}

	// Connect to server
	if err := w.Connect(); err != nil {
		return err
	}

	// Start batch sender
	go w.batchSender()

	// Setup filesystem watcher FIRST so we catch any changes during initial scan
	if err := w.setupFSWatcher(); err != nil {
		return err
	}
	defer w.fsWatcher.Close()

	// Start handling filesystem events BEFORE initial scan
	// This ensures we don't miss events that happen during the scan
	go w.handleFSEvents()

	// Initial scan
	log.Printf("Scanning directory: %s", w.watchDir)
	if err := w.scanDirectory(); err != nil {
		return err
	}

	// Wait for batch to flush
	time.Sleep(time.Duration(w.batchMs*2) * time.Millisecond)

	log.Printf("Initial scan complete. Watching for changes... (Ctrl+C to stop)")

	// Wait for interrupt signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	log.Printf("Shutting down...")
	close(w.done)

	// Give batch sender time to finish
	time.Sleep(time.Duration(w.batchMs*2) * time.Millisecond)

	return nil
}

func main() {
	watchDir := flag.String("watch", "", "Directory to watch (required)")
	serverURL := flag.String("server", "ws://localhost:7164/watch", "WebSocket server URL")
	batchMs := flag.Int("batch-ms", 100, "Batch interval in milliseconds")
	flag.Parse()

	if *watchDir == "" {
		log.Fatal("--watch flag is required")
	}

	watcher := NewWatcher(*watchDir, *serverURL, *batchMs)
	if err := watcher.Run(); err != nil {
		log.Fatalf("Watcher failed: %v", err)
	}
}
