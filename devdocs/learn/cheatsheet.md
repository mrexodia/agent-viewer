# Agent Viewer Cheat Sheet

Quick reference for working on this codebase.

## Architecture At a Glance

```
┌──────────────┐     WebSocket      ┌──────────────┐       SSE        ┌──────────────┐
│   Watcher    │ ──────────────────►│    Server    │ ────────────────►│   Browser    │
│  (Go/fsnotify)                    │  (Go/memory) │                  │  (HTML/JS)   │
└──────────────┘                    └──────────────┘                  └──────────────┘
       │                                   │                                 │
   monitors                            stores                           displays
       │                                   │                                 │
   .jsonl files                    sessions in RAM                    live updates
```

## Key Files

| File | Purpose | Key Functions |
|------|---------|---------------|
| `watcher/main.go` | File monitoring & streaming | `readFile()`, `batchSender()`, `handleFSEvents()` |
| `server/main.go` | HTTP/WS server, SSE broadcasting | `handleWatch()`, `Broadcast()`, `handleSessionStream()` |
| `server/static/index.html` | Web UI | `connectToStream()`, `handleNewLine()` |
| `tests/integration_test.go` | End-to-end tests | All `Test*` functions |

## Key Data Structures

### Watcher
```go
type FileState struct {
    Path     string  // Relative path
    LastLine int     // Last line sent
    LastSize int64   // For truncation detection
}

type LineMessage struct {
    Type string `json:"type"`  // "line"
    Path string `json:"path"`  // "session.jsonl"
    Line string `json:"line"`  // Raw JSON line
}
```

### Server
```go
type Session struct {
    Path      string
    Lines     []string    // All accumulated lines
    UpdatedAt time.Time
    mu        sync.RWMutex
}

type SSEClient struct {
    path   string          // Which session (empty = all)
    events chan LineEvent  // Buffered channel
}
```

## API Quick Reference

```bash
# List all sessions
curl http://localhost:7164/api/sessions

# Get session content
curl http://localhost:7164/api/sessions/my-session.jsonl

# Stream session updates (SSE)
curl http://localhost:7164/api/sessions/my-session.jsonl/stream

# Stream all sessions (SSE)
curl http://localhost:7164/api/stream
```

## Common Commands

```bash
# Build everything
make build

# Run tests
make test

# Start server (default port 7164)
cd server && go run .

# Start watcher
cd watcher && go run . --watch ~/.pi/agent/sessions --server ws://localhost:7164/watch

# Test manually
echo '{"event":"test"}' >> /path/to/session.jsonl
```

## Design Decisions to Remember

| Decision | Rationale |
|----------|-----------|
| In-memory only | MVP simplicity, add persistence in Phase 2 |
| Block on slow client | Prefer "no data loss" over throughput |
| 100ms batching | Balance latency vs efficiency |
| bufio.Reader not Scanner | Handle lines >64KB (base64 images) |
| SSE not WebSocket to browser | Simpler, auto-reconnect in browsers |

## Edge Cases Handled

- **File truncation**: Reset position, resend all
- **Connection loss**: Exponential backoff (1s, 2s, 4s... max 30s)
- **Path traversal**: Reject any path with `..`
- **New subdirectories**: Automatically watched via fsnotify CREATE
- **Long lines**: Tested up to 5MB

## Performance Targets

- Latency: <500ms file-to-browser
- Sessions: 100+ concurrent
- Lines: 100,000+ per session
- Viewers: 10+ concurrent browsers
- Memory: ~1KB per line

## Debugging Tips

1. **Server not receiving?** Check watcher output for connection errors
2. **Browser not updating?** Open DevTools Network tab, check SSE connection
3. **Slow updates?** Lower `--batch-ms` on watcher
4. **Memory growing?** Each line stored forever (MVP limitation)

## Test Fixtures

```go
// 10-line test session
var testDataSingle = []string{
    `{"event":"start","timestamp":"2026-01-04T10:00:00Z"}`,
    `{"event":"tool_call","tool":"web_search","query":"test"}`,
    // ... 8 more lines
}
```
