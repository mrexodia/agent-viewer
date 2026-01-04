package main

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// LineMessage is the message format from watcher
type LineMessage struct {
	Type string `json:"type"` // Always "line"
	Path string `json:"path"` // Relative path
	Line string `json:"line"` // Raw JSONL line content
}

// LineEvent is sent to SSE clients
type LineEvent struct {
	Path    string `json:"path"`
	Line    string `json:"line"`
	LineNum int    `json:"line_num"`
}

// Session stores data for a single JSONL session file
type Session struct {
	Path       string    `json:"path"`
	Lines      []string  `json:"lines,omitempty"`
	RawContent []byte    `json:"-"` // Complete raw file content
	LineCount  int       `json:"line_count"`
	UpdatedAt  time.Time `json:"updated_at"`
	mu         sync.RWMutex
}

// SSEClient represents a connected SSE client
type SSEClient struct {
	path   string
	events chan LineEvent
}

// SSEBroadcaster manages SSE client connections
type SSEBroadcaster struct {
	clients map[*SSEClient]bool
	mu      sync.RWMutex
}

// NewSSEBroadcaster creates a new broadcaster
func NewSSEBroadcaster() *SSEBroadcaster {
	return &SSEBroadcaster{
		clients: make(map[*SSEClient]bool),
	}
}

// Subscribe adds a new SSE client
func (b *SSEBroadcaster) Subscribe(path string) *SSEClient {
	client := &SSEClient{
		path:   path,
		events: make(chan LineEvent, 100),
	}
	b.mu.Lock()
	b.clients[client] = true
	b.mu.Unlock()
	return client
}

// Unsubscribe removes an SSE client
func (b *SSEBroadcaster) Unsubscribe(client *SSEClient) {
	b.mu.Lock()
	delete(b.clients, client)
	b.mu.Unlock()
	close(client.events)
}

// Broadcast sends an event to all clients watching a path
func (b *SSEBroadcaster) Broadcast(event LineEvent) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	for client := range b.clients {
		// Send to clients watching this specific path or all paths (empty path)
		if client.path == "" || client.path == event.Path {
			select {
			case client.events <- event:
			default:
				// Client buffer full, skip
			}
		}
	}
}

// SessionStore manages all sessions in memory
type SessionStore struct {
	sessions map[string]*Session
	mu       sync.RWMutex
}

// NewSessionStore creates a new session store
func NewSessionStore() *SessionStore {
	return &SessionStore{
		sessions: make(map[string]*Session),
	}
}

// AddLine adds a line to a session, creating it if necessary
func (s *SessionStore) AddLine(path, line string) int {
	s.mu.Lock()
	session, exists := s.sessions[path]
	if !exists {
		session = &Session{
			Path:       path,
			Lines:      make([]string, 0),
			RawContent: make([]byte, 0),
		}
		s.sessions[path] = session
	}
	s.mu.Unlock()

	session.mu.Lock()
	defer session.mu.Unlock()

	// Accumulate raw content (preserve original line with newline)
	rawLine := line
	if !strings.HasSuffix(rawLine, "\n") {
		rawLine += "\n"
	}
	session.RawContent = append(session.RawContent, []byte(rawLine)...)

	// Compute MD5 hash of the line for debugging
	// Verify with: printf '{"event":"..."}\n' | md5
	hash := md5.Sum([]byte(rawLine))
	hashStr := hex.EncodeToString(hash[:])

	// Strip trailing newline for Lines array storage
	line = strings.TrimSuffix(line, "\n")
	session.Lines = append(session.Lines, line)
	session.UpdatedAt = time.Now()
	lineNum := len(session.Lines)

	log.Printf("[%s] line %d md5=%s", path, lineNum, hashStr)

	return lineNum
}

// GetSession returns a session by path
func (s *SessionStore) GetSession(path string) *Session {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.sessions[path]
}

// ListSessions returns metadata for all sessions
func (s *SessionStore) ListSessions() []Session {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]Session, 0, len(s.sessions))
	for _, session := range s.sessions {
		session.mu.RLock()
		result = append(result, Session{
			Path:      session.Path,
			LineCount: len(session.Lines),
			UpdatedAt: session.UpdatedAt,
		})
		session.mu.RUnlock()
	}
	return result
}

// Server handles HTTP and WebSocket connections
type Server struct {
	store       *SessionStore
	port        int
	upgrader    websocket.Upgrader
	broadcaster *SSEBroadcaster
}

// NewServer creates a new server instance
func NewServer(port int) *Server {
	return &Server{
		store:       NewSessionStore(),
		port:        port,
		broadcaster: NewSSEBroadcaster(),
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool {
				return true // Allow all origins for MVP
			},
		},
	}
}

// handleWatch handles WebSocket connections from watchers
func (s *Server) handleWatch(w http.ResponseWriter, r *http.Request) {
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("WebSocket upgrade failed: %v", err)
		return
	}
	defer conn.Close()

	log.Printf("Watcher connected from %s", r.RemoteAddr)

	for {
		_, message, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("WebSocket error: %v", err)
			}
			break
		}

		var msg LineMessage
		if err := json.Unmarshal(message, &msg); err != nil {
			log.Printf("Invalid message format: %v", err)
			continue
		}

		if msg.Type == "line" {
			lineNum := s.store.AddLine(msg.Path, msg.Line)
			log.Printf("Received line %d for %s", lineNum, msg.Path)

			// Broadcast to SSE clients
			s.broadcaster.Broadcast(LineEvent{
				Path:    msg.Path,
				Line:    strings.TrimSuffix(msg.Line, "\n"),
				LineNum: lineNum,
			})
		}
	}

	log.Printf("Watcher disconnected from %s", r.RemoteAddr)
}

// handleIndex serves the static HTML page
func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/" {
		http.ServeFile(w, r, "static/index.html")
		return
	}
	http.ServeFile(w, r, "static"+r.URL.Path)
}

// handleSessions returns the list of sessions
func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	sessions := s.store.ListSessions()

	response := struct {
		Sessions []Session `json:"sessions"`
	}{
		Sessions: sessions,
	}

	if err := json.NewEncoder(w).Encode(response); err != nil {
		log.Printf("Error encoding sessions: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	}
}

// handleSessionContent returns the content of a specific session
func (s *Server) handleSessionContent(w http.ResponseWriter, r *http.Request) {
	// Extract path from URL: /api/sessions/{path}
	path := strings.TrimPrefix(r.URL.Path, "/api/sessions/")
	if path == "" {
		http.Error(w, "Session path required", http.StatusBadRequest)
		return
	}

	// Check if this is a stream request
	if strings.HasSuffix(path, "/stream") {
		streamPath := strings.TrimSuffix(path, "/stream")
		s.handleSessionStream(w, r, streamPath)
		return
	}

	session := s.store.GetSession(path)
	if session == nil {
		http.Error(w, "Session not found", http.StatusNotFound)
		return
	}

	session.mu.RLock()
	defer session.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	response := struct {
		Path  string   `json:"path"`
		Lines []string `json:"lines"`
	}{
		Path:  session.Path,
		Lines: session.Lines,
	}

	if err := json.NewEncoder(w).Encode(response); err != nil {
		log.Printf("Error encoding session: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	}
}

// handleSessionStream handles SSE connections for live updates
func (s *Server) handleSessionStream(w http.ResponseWriter, r *http.Request, path string) {
	// Set SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	// Get flusher for streaming
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming not supported", http.StatusInternalServerError)
		return
	}

	// Subscribe to events
	client := s.broadcaster.Subscribe(path)
	defer s.broadcaster.Unsubscribe(client)

	log.Printf("SSE client connected for path: %s", path)

	// Send existing lines first if session exists
	if path != "" {
		session := s.store.GetSession(path)
		if session != nil {
			session.mu.RLock()
			for i, line := range session.Lines {
				event := LineEvent{
					Path:    path,
					Line:    line,
					LineNum: i + 1,
				}
				data, _ := json.Marshal(event)
				fmt.Fprintf(w, "event: line\ndata: %s\n\n", data)
			}
			session.mu.RUnlock()
			flusher.Flush()
		}
	}

	// Stream new events
	for {
		select {
		case event, ok := <-client.events:
			if !ok {
				return
			}
			data, _ := json.Marshal(event)
			fmt.Fprintf(w, "event: line\ndata: %s\n\n", data)
			flusher.Flush()

		case <-r.Context().Done():
			log.Printf("SSE client disconnected for path: %s", path)
			return
		}
	}
}

// handleGlobalStream handles SSE for all session updates
func (s *Server) handleGlobalStream(w http.ResponseWriter, r *http.Request) {
	s.handleSessionStream(w, r, "") // Empty path means all sessions
}

// Start runs the HTTP server
func (s *Server) Start() error {
	mux := http.NewServeMux()

	// WebSocket endpoint for watchers
	mux.HandleFunc("/watch", s.handleWatch)

	// API endpoints
	mux.HandleFunc("/api/stream", s.handleGlobalStream)      // Global stream for all sessions
	mux.HandleFunc("/api/sessions/", s.handleSessionContent) // Must be before /api/sessions
	mux.HandleFunc("/api/sessions", s.handleSessions)

	// Static files (index.html and any other assets)
	mux.HandleFunc("/", s.handleIndex)

	addr := fmt.Sprintf(":%d", s.port)
	log.Printf("Server starting on http://localhost%s", addr)
	return http.ListenAndServe(addr, mux)
}

func main() {
	port := flag.Int("port", 7164, "HTTP server port")
	flag.Parse()

	server := NewServer(*port)
	if err := server.Start(); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}
