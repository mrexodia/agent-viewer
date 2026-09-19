package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// LineEvent is sent to SSE clients
type LineEvent struct {
	Path    string `json:"path"`
	Line    string `json:"line"`
	LineNum int    `json:"line_num"`
	Source  string `json:"source"`
}

// Session stores data for a single JSONL session file
type Session struct {
	Path       string    `json:"path"`
	Source     string    `json:"source,omitempty"` // Display/parser label: pi, claude, etc.
	SourceID   string    `json:"-"`                // Stable watch-root identity, not the display label.
	Preview    string    `json:"preview,omitempty"`
	Generation string    `json:"generation"`
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
		events: make(chan LineEvent, 1), // Coalesced wakeups, not the authoritative event log.
	}
	b.mu.Lock()
	b.clients[client] = true
	b.mu.Unlock()
	return client
}

// Unsubscribe removes an SSE client
func (b *SSEBroadcaster) Unsubscribe(client *SSEClient) {
	b.mu.Lock()
	if b.clients[client] {
		delete(b.clients, client)
		close(client.events)
	}
	b.mu.Unlock()
}

// Broadcast sends an event to all clients watching a path
func (b *SSEBroadcaster) Broadcast(event LineEvent) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	for client := range b.clients {
		// Send to clients watching this specific path or all paths (empty path)
		if client.path == "" || client.path == event.Path {
			// A pending wakeup is sufficient: handlers read every missing line
			// from the store. Slow clients cannot block producers or cleanup.
			select {
			case client.events <- event:
			default:
			}
		}
	}
}

// SessionStore manages all sessions in memory
type SessionStore struct {
	sessions map[string]*Session
	debug    bool
	mu       sync.RWMutex
}

// NewSessionStore creates a new session store
func NewSessionStore(debug bool) *SessionStore {
	return &SessionStore{
		sessions: make(map[string]*Session),
		debug:    debug,
	}
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
			Path:       session.Path,
			Source:     session.Source,
			Preview:    session.Preview,
			Generation: session.Generation,
			LineCount:  len(session.Lines),
			UpdatedAt:  session.UpdatedAt,
		})
		session.mu.RUnlock()
	}
	return result
}

// Server handles HTTP and WebSocket connections
type Server struct {
	store       *SessionStore
	port        int
	debug       bool
	upgrader    websocket.Upgrader
	broadcaster *SSEBroadcaster
}

// NewServer creates a new server instance
func NewServer(port int, debug bool) *Server {
	return &Server{
		store:       NewSessionStore(debug),
		port:        port,
		debug:       debug,
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

		var msg SyncMessage
		if err := json.Unmarshal(message, &msg); err != nil {
			return
		}
		reply, changed := s.store.Apply(msg)
		if changed {
			s.broadcaster.Broadcast(LineEvent{Path: msg.Path})
		}
		// Acknowledgements mean committed to the in-memory store. If this
		// connection dies before the ACK arrives, sync reconciles the prefix.
		conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
		if err := conn.WriteJSON(reply); err != nil {
			return
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

	// URL decode the path
	decodedPath, err := url.PathUnescape(path)
	if err != nil {
		http.Error(w, "Invalid path encoding", http.StatusBadRequest)
		return
	}
	path = decodedPath

	// Normalize path separators to forward slashes
	path = strings.ReplaceAll(path, "\\", "/")

	// Security: reject path traversal attempts
	if strings.Contains(path, "..") {
		http.Error(w, "Invalid path", http.StatusBadRequest)
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
	lines := append([]string{}, session.Lines...)
	session.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	response := struct {
		Path   string   `json:"path"`
		Lines  []string `json:"lines"`
		Source string   `json:"source"`
	}{
		Path:   session.Path,
		Lines:  lines,
		Source: session.Source,
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
	if _, ok := w.(http.Flusher); !ok {
		http.Error(w, "Streaming not supported", http.StatusInternalServerError)
		return
	}

	// Subscribe before the snapshot. Notifications are only wakeups; a cursor
	// into the store makes snapshot/live overlap and coalescing lossless.
	client := s.broadcaster.Subscribe(path)
	defer s.broadcaster.Unsubscribe(client)
	controller := http.NewResponseController(w)
	send := func(event, id string, value any) error {
		if err := r.Context().Err(); err != nil {
			return err
		}
		_ = controller.SetWriteDeadline(time.Now().Add(5 * time.Second))
		data, err := json.Marshal(value)
		if err != nil {
			return err
		}
		if id != "" {
			if _, err := fmt.Fprintf(w, "id: %s\n", id); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data); err != nil {
			return err
		}
		return controller.Flush()
	}
	generation, cursor := "", 0
	if parts := strings.Split(r.Header.Get("Last-Event-ID"), ":"); len(parts) == 2 {
		if n, err := strconv.Atoi(parts[1]); err == nil && n >= 0 {
			generation, cursor = parts[0], n
		}
	}
	heartbeat := time.NewTicker(10 * time.Second)
	defer heartbeat.Stop()
	for {
		if path == "" {
			// Global consumers need current metadata, not another copy of every
			// transcript line. A complete snapshot also reconciles reconnects.
			if err := send("sessions", "", s.store.ListSessions()); err != nil {
				return
			}
		} else {
			for {
				session := s.store.GetSession(path)
				if session == nil {
					break
				}
				session.mu.RLock()
				gen, total, source := session.Generation, len(session.Lines), session.Source
				reset := generation != gen || cursor > total
				if reset {
					generation, cursor = gen, 0
				}
				end := cursor + 128
				if end > total {
					end = total
				}
				lines := append([]string(nil), session.Lines[cursor:end]...)
				session.mu.RUnlock()
				if reset {
					if err := send("reset", gen+":0", map[string]string{"path": path}); err != nil {
						return
					}
				}
				for _, line := range lines {
					cursor++
					if err := send("line", fmt.Sprintf("%s:%d", gen, cursor), LineEvent{Path: path, Line: line, LineNum: cursor, Source: source}); err != nil {
						return
					}
				}
				if cursor == total {
					break
				}
			}
		}
		// Flush headers even for a nonexistent/empty session and keep idle
		// connections alive. Never hold store/broadcaster locks during I/O.
		_ = controller.SetWriteDeadline(time.Now().Add(5 * time.Second))
		if _, err := fmt.Fprint(w, ": ready\n\n"); err != nil {
			return
		}
		if err := controller.Flush(); err != nil {
			return
		}
		select {
		case _, ok := <-client.events:
			if !ok {
				return
			}
		case <-heartbeat.C:
		case <-r.Context().Done():
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
	debug := flag.Bool("debug", false, "Enable batch commit logging")
	flag.Parse()

	server := NewServer(*port, *debug)
	if err := server.Start(); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}
