# MVP Epic - Archive

## Goal and Scope

Built a real-time agent session viewer that monitors JSONL log files and streams updates to a web browser with <500ms latency. The system consists of three components: a file watcher that detects changes and sends them via WebSocket, a server that stores sessions in memory and broadcasts via SSE, and a web UI with both raw and pretty-printed views of agent conversations.

## Key Architectural Decisions

1. **Three-process architecture**: Watcher → Server → Browser. This allows the watcher to run on one machine and server on another (future multi-machine support).

2. **WebSocket for watcher→server, SSE for server→browser**: WebSocket provides bidirectional capability for future sync protocols. SSE is simpler for browser clients and handles reconnection well.

3. **In-memory storage only**: No database for MVP. Sessions stored in `map[string]*Session`. Acceptable for <100k lines per session.

4. **100ms batching in watcher**: Reduces WebSocket message overhead during rapid file updates without significant latency impact.

5. **Lines stored without parsing**: Server treats JSONL lines as opaque strings. Parsing happens only in the browser's "Pretty" view. This keeps the server simple and fast.

## Technical Insights and Gotchas

1. **fsnotify doesn't recurse into new directories**: When a new directory is created, you must recursively walk it and add all subdirectories to the watcher. The initial implementation missed nested directories created after startup.

2. **File truncation detection**: Track `LastSize` per file. If current size < last size, the file was truncated - reset position and resend all lines.

3. **Port conflicts in tests**: Using `os.Getpid()%1000` gives the same port for all tests in one process. Use an atomic counter instead for unique ports per test.

4. **Cross-platform test binaries**: Don't hardcode binary paths like `server/server`. Use `go run .` which works on Windows (.exe) and Unix.

5. **SSE existing lines**: When a browser connects to a session stream, send all existing lines first, then stream new ones. Otherwise the client misses history.

6. **MD5 for debugging**: Print MD5 hash of each line on the server. Verify with `printf 'line\n' | md5` on macOS.

## API Summary

### Server Endpoints

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/` | GET | Serve web UI |
| `/watch` | WebSocket | Watcher connection |
| `/api/sessions` | GET | List all sessions |
| `/api/sessions/{path}` | GET | Get session content |
| `/api/sessions/{path}/stream` | GET (SSE) | Stream session updates |
| `/api/stream` | GET (SSE) | Stream all session updates |

### WebSocket Protocol (Watcher → Server)

```json
{"type":"line","path":"session.jsonl","line":"{\"event\":\"start\"}\n"}
```

### SSE Events (Server → Browser)

```
event: line
data: {"path":"session.jsonl","line":"{\"event\":\"start\"}","line_num":1}
```

## Code Walkthrough

### server/main.go
- `SessionStore`: In-memory storage with `map[string]*Session`
- `AddLine()`: Appends line, computes MD5, broadcasts to SSE clients
- `SSEBroadcaster`: Manages SSE client subscriptions per path
- `handleWatch()`: WebSocket handler for watcher connections
- `handleSessionStream()`: SSE handler, sends existing lines then streams new

### watcher/main.go
- `FileState`: Tracks `LastLine` and `LastSize` per file
- `scanDirectory()`: Initial recursive scan for .jsonl files
- `addDirectoryRecursive()`: Handles new directories at runtime
- `handleFSEvents()`: Processes fsnotify events, reads new lines
- `batchSender()`: Collects lines for 100ms before sending
- `Reconnect()`: Exponential backoff (1s, 2s, 4s... max 30s)

### server/static/index.html
- Two tabs: "Pretty" (parsed messages) and "Raw" (line-by-line)
- `renderMessage()`: Formats user/assistant messages, tool calls, thinking
- `connectToStream()`: SSE connection with auto-reconnect
- `connectGlobalStream()`: Updates session list in real-time

### tests/integration_test.go
- `TestEnv`: Manages server/watcher processes with `go run`
- `findProjectRoot()`: Locates project by searching for server/watcher dirs
- Unique port per test via atomic counter
- Tests cover: initial scan, live updates, nested dirs, latency

## Validation

```bash
# Run all tests
make test

# Manual testing
make server                    # Terminal 1
make watcher WATCH_DIR=/tmp/test  # Terminal 2
# Open http://localhost:7164

# Append and watch
echo '{"event":"test"}' >> /tmp/test/session.jsonl
```

Expected: Line appears in browser within 500ms.

## Performance

| Metric | Achieved |
|--------|----------|
| Latency (file → browser) | ~54ms |
| Target latency | <500ms |
| Test count | 11 |
| Test duration | ~8s |

## References

- Design doc: promoted to [devdocs/design.md](../design.md)
- Test specifications: archived (was devdocs/mvp/tests.md)
- Implementation guide: archived (was devdocs/mvp/implementation.md)
