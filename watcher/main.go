package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/gorilla/websocket"
)

// FileState is an acknowledged prefix, never merely a read/queued position.
// Incomplete trailing records stay in the source file until their LF is written.
type FileState struct {
	SyncReply
	info os.FileInfo
}

// WatchDir associates a session format/label with a filesystem root.
type WatchDir struct {
	Path   string
	Source string
	id     string
}

// Watcher has one owner loop for filesystem reads and WebSocket exchanges.
// Files, rather than an unbounded RAM queue, are the durable retry buffer.
type Watcher struct {
	watchDirs   []WatchDir
	serverURL   string
	batchMs     int
	conn        *websocket.Conn
	files       map[string]*FileState
	fsWatcher   *fsnotify.Watcher
	directories map[string]bool
}

func NewWatcher(watchDirs []WatchDir, serverURL string, batchMs int) *Watcher {
	return &Watcher{watchDirs: watchDirs, serverURL: serverURL, batchMs: batchMs, files: make(map[string]*FileState), directories: make(map[string]bool)}
}

// Prefer the most specific root and require a path-component boundary (not a
// string prefix, which would confuse /sessions with /sessions-other).
func (w *Watcher) findWatchDirForPath(absPath string) *WatchDir {
	var best *WatchDir
	for i := range w.watchDirs {
		root := &w.watchDirs[i]
		rel, err := filepath.Rel(root.Path, absPath)
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel) {
			if best == nil || len(root.Path) > len(best.Path) {
				best = root
			}
		}
	}
	return best
}

func (w *Watcher) getRelPathAndSource(absPath string) (string, string, error) {
	root := w.findWatchDirForPath(absPath)
	if root == nil {
		return "", "", fmt.Errorf("path not in any watch directory: %s", absPath)
	}
	rel, err := filepath.Rel(root.Path, absPath)
	if err != nil {
		return "", "", err
	}
	return root.Source + "/" + normalizePath(rel), root.Source, nil
}

func normalizePath(path string) string { return strings.ReplaceAll(path, "\\", "/") }

func (w *Watcher) exchange(msg SyncMessage) (SyncReply, error) {
	deadline := time.Now().Add(5 * time.Second)
	w.conn.SetWriteDeadline(deadline)
	w.conn.SetReadDeadline(deadline)
	if err := w.conn.WriteJSON(msg); err != nil {
		return SyncReply{}, err
	}
	var reply SyncReply
	if err := w.conn.ReadJSON(&reply); err != nil {
		return reply, err
	}
	if reply.Error != "" {
		return reply, fmt.Errorf("server: %s", reply.Error)
	}
	if (msg.Type == "ping" && reply.Type != "pong") || (msg.Type != "ping" && (reply.Type != "ack" || reply.Generation == "" || reply.Offset < 0)) {
		return reply, fmt.Errorf("invalid server acknowledgement")
	}
	return reply, nil
}

// readFile verifies the already-committed prefix, then sends bounded batches of
// complete records. Prefix verification also detects same-size replacement and
// truncate-and-regrow events that file size/inode checks alone cannot detect.
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
	root := w.findWatchDirForPath(absPath)
	if root == nil {
		return fmt.Errorf("path not in any watch directory: %s", absPath)
	}
	exchange := func(msg SyncMessage) (SyncReply, error) {
		msg.Source, msg.SourceID = root.Source, root.id
		msg.ModTime = info.ModTime().UTC().Format(time.RFC3339Nano)
		return w.exchange(msg)
	}
	state := w.files[relPath]
	if state == nil {
		reply, err := exchange(SyncMessage{Type: "sync", Path: relPath})
		if err != nil {
			return err
		}
		state = &FileState{SyncReply: reply}
		w.files[relPath] = state
	}
	hash := sha256.New()
	n, err := io.CopyN(hash, file, state.Offset)
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	if n != state.Offset || hex.EncodeToString(hash.Sum(nil)) != state.Digest {
		reply, err := exchange(SyncMessage{Type: "reset", Path: relPath, Generation: state.Generation, Offset: state.Offset})
		if err != nil {
			return err
		}
		state.SyncReply = reply
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			return err
		}
		hash.Reset()
	}
	// Bound this pass to the observed size; continuously growing files must not
	// starve other sessions or cancellation. Later changes trigger another pass.
	remaining := info.Size() - state.Offset
	if remaining < 0 {
		remaining = 0
	}
	reader := bufio.NewReader(io.LimitReader(file, remaining))
	for {
		lines, eof, err := readBatch(reader)
		if err != nil {
			return err
		}
		if len(lines) > 0 {
			offset := state.Offset
			for _, line := range lines {
				offset += int64(len(line))
			}
			reply, err := exchange(SyncMessage{Type: "append", Path: relPath, Generation: state.Generation, Offset: state.Offset, Lines: lines})
			if err != nil {
				return err
			}
			if reply.Generation != state.Generation || reply.Offset != offset {
				return fmt.Errorf("acknowledged prefix changed; resync required")
			}
			for _, line := range lines {
				_, _ = io.WriteString(hash, line)
			}
			state.SyncReply = reply
			state.Digest = hex.EncodeToString(hash.Sum(nil))
		}
		if eof {
			break
		}
	}
	state.info = info
	return nil
}

// An individual record can exceed the byte budget (e.g. image/tool output).
// No newline is invented at EOF, and no fixed Scanner token limit is imposed.
func readBatch(reader *bufio.Reader) ([]string, bool, error) {
	var lines []string
	size := 0
	for len(lines) < 128 && size < 1024*1024 {
		line, err := reader.ReadString('\n')
		if errors.Is(err, io.EOF) {
			return lines, true, nil
		}
		if err != nil {
			return nil, false, err
		}
		lines = append(lines, line)
		size += len(line)
	}
	return lines, false, nil
}

func (w *Watcher) scanDirectory() error {
	for _, root := range w.watchDirs {
		err := filepath.Walk(root.Path, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				if !errors.Is(err, os.ErrNotExist) {
					log.Printf("Cannot scan %s (will retry): %v", path, err)
				}
				return nil // One inaccessible path must not starve other sessions.
			}
			if info.IsDir() {
				if !w.directories[path] {
					if err := w.fsWatcher.Add(path); err != nil {
						log.Printf("Cannot watch %s (polling instead): %v", path, err)
					} else {
						w.directories[path] = true
					}
				}
				return nil
			}
			if !strings.HasSuffix(path, ".jsonl") {
				return nil
			}
			relPath, _, err := w.getRelPathAndSource(path)
			if err != nil {
				return err
			}
			relPath = normalizePath(relPath)
			state := w.files[relPath]
			if state != nil && state.info != nil && os.SameFile(info, state.info) && info.Size() == state.info.Size() && info.ModTime().Equal(state.info.ModTime()) {
				return nil
			}
			if err := w.readFile(path, relPath); err != nil {
				if errors.Is(err, os.ErrNotExist) {
					return nil
				}
				var fileError *os.PathError
				if errors.As(err, &fileError) {
					log.Printf("Cannot read %s (will retry): %v", relPath, err)
					return nil
				}
				return fmt.Errorf("%s: %w", relPath, err)
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func (w *Watcher) RunContext(ctx context.Context) error {
	if w.batchMs <= 0 {
		return fmt.Errorf("--batch-ms must be positive")
	}
	host, err := os.Hostname()
	if err != nil {
		return err
	}
	var roots []WatchDir
	labels := make(map[string]bool)
	for _, root := range w.watchDirs {
		if err := validateSource(root.Source); err != nil {
			return err
		}
		if labels[root.Source] {
			return fmt.Errorf("duplicate source label %q: give each watch root a distinct label", root.Source)
		}
		labels[root.Source] = true
		root.Path, err = filepath.Abs(expandPath(root.Path))
		if err != nil {
			return err
		}
		root.Path, err = filepath.EvalSymlinks(root.Path)
		if errors.Is(err, os.ErrNotExist) {
			log.Printf("Skipping missing watch directory for %s", root.Source)
			continue
		}
		if err != nil {
			return err
		}
		info, err := os.Stat(root.Path)
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return fmt.Errorf("watch root is not a directory: %s", root.Path)
		}
		identity := sha256.Sum256([]byte(host + "\x00" + normalizePath(root.Path)))
		root.id = hex.EncodeToString(identity[:])
		roots = append(roots, root)
	}
	if len(roots) == 0 {
		return fmt.Errorf("no existing watch directories")
	}
	w.watchDirs = roots
	w.fsWatcher, err = fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer w.fsWatcher.Close()
	ticker := time.NewTicker(time.Duration(w.batchMs) * time.Millisecond)
	defer ticker.Stop()
	backoff := 100 * time.Millisecond
	var retryAt, lastScan, lastPing time.Time
	dirty := true
	var stopCancel func() bool
	disconnect := func() {
		if stopCancel != nil {
			stopCancel()
			stopCancel = nil
		}
		if w.conn != nil {
			w.conn.Close()
			w.conn = nil
		}
		w.files = make(map[string]*FileState) // Next connection must reconcile every file.
	}
	defer disconnect()
	for {
		if ctx.Err() != nil {
			return nil
		}
		if w.conn == nil && !time.Now().Before(retryAt) {
			dialer := websocket.Dialer{HandshakeTimeout: 5 * time.Second}
			conn, _, err := dialer.DialContext(ctx, w.serverURL, nil)
			if err != nil {
				log.Printf("Connection failed: %v; retrying in %v", err, backoff)
				retryAt = time.Now().Add(backoff)
				backoff = min(backoff*2, 5*time.Second)
			} else {
				w.conn = conn
				// Cancellation interrupts pending socket I/O, not just the outer select.
				stopCancel = context.AfterFunc(ctx, func() { conn.Close() })
				lastScan, lastPing = time.Time{}, time.Time{}
				dirty = true
				log.Printf("Connected to server: %s", w.serverURL)
			}
		}
		if w.conn != nil {
			var err error
			if time.Since(lastPing) >= time.Second {
				_, err = w.exchange(SyncMessage{Type: "ping"})
				lastPing = time.Now()
			}
			if err == nil && (dirty || time.Since(lastScan) >= time.Second) {
				initial := lastScan.IsZero()
				err = w.scanDirectory()
				if err == nil {
					lastScan, dirty = time.Now(), false
					backoff = 100 * time.Millisecond
					if initial {
						log.Printf("Initial scan complete. Watching for changes...")
					}
				}
			}
			if err != nil {
				log.Printf("Sync interrupted: %v; retrying in %v", err, backoff)
				disconnect()
				retryAt = time.Now().Add(backoff)
				backoff = min(backoff*2, 5*time.Second)
			}
		}
		// Coalesce filesystem events until the next flush tick. Polling is also a
		// fallback for missed/overflowed events and directories created mid-scan.
	wait:
		for {
			select {
			case <-ctx.Done():
				return nil
			case event, ok := <-w.fsWatcher.Events:
				if !ok {
					return fmt.Errorf("filesystem watcher closed")
				}
				dirty = true
				if event.Op&(fsnotify.Remove|fsnotify.Rename) != 0 {
					delete(w.directories, event.Name)
				}
				if strings.HasSuffix(event.Name, ".jsonl") {
					if rel, _, err := w.getRelPathAndSource(event.Name); err == nil {
						if state := w.files[normalizePath(rel)]; state != nil {
							state.info = nil
						}
					}
				}
			case err, ok := <-w.fsWatcher.Errors:
				if !ok {
					return fmt.Errorf("filesystem watcher closed")
				}
				log.Printf("Filesystem event error (rescanning): %v", err)
				dirty = true
			case <-ticker.C:
				break wait
			}
		}
	}
}

func (w *Watcher) Run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return w.RunContext(ctx)
}

func main() {
	var specs arrayFlags
	flag.Var(&specs, "watch", "Directory to watch (source:path or path); may be repeated")
	serverURL := flag.String("server", "ws://localhost:7164/watch", "WebSocket server URL")
	batchMs := flag.Int("batch-ms", 100, "Batch interval in milliseconds")
	usePi := flag.Bool("pi", false, "Watch Pi sessions at ~/.pi/agent/sessions")
	useClaude := flag.Bool("claude", false, "Watch Claude Code sessions at ~/.claude/projects")
	flag.Parse()
	var dirs []WatchDir
	if *usePi {
		dirs = append(dirs, WatchDir{Path: expandPath("~/.pi/agent/sessions"), Source: "pi"})
	}
	if *useClaude {
		dirs = append(dirs, WatchDir{Path: expandPath("~/.claude/projects"), Source: "claude"})
	}
	for _, spec := range specs {
		dir, err := parseWatchSpec(spec)
		if err != nil {
			log.Fatal(err)
		}
		dirs = append(dirs, dir)
	}
	if len(dirs) == 0 {
		log.Fatal("No watch directories specified. Use --pi, --claude, or --watch source:path")
	}
	if err := NewWatcher(dirs, *serverURL, *batchMs).Run(); err != nil {
		log.Fatal(err)
	}
}
