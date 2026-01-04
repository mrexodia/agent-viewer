# Agent Session Viewer - Design Document

## Overview

A real-time session monitoring system for agent workflows that write JSONL event logs. The system consists of:
1. **Watcher** - Monitors local .jsonl files and streams updates
2. **Server** - Receives updates and broadcasts to web clients
3. **Web UI** - Displays live session updates in browser

## Goals

### Primary Goal
Enable real-time monitoring of agent sessions with <500ms latency from file write to browser display.

### Secondary Goals
- Support multiple concurrent viewers
- Handle offline/reconnection gracefully
- Scale to 100+ session files
- Work cross-platform (Windows, macOS, Linux)

### Non-Goals (MVP)
- Authentication/authorization
- Persistent storage/database
- Session hierarchy parsing
- Historical session search
- Multi-machine deployment

## Architecture

```
┌──────────────────────────┐
│  File System             │
│  ~/.pi/agent/sessions/   │
│    ├── session1.jsonl    │
│    └── session2.jsonl    │
└────────┬─────────────────┘
         │ (fsnotify)
         ↓
┌──────────────────────────┐
│  Watcher Process         │
│  - Monitors directory    │
│  - Reads new lines       │
│  - Batches updates       │
└────────┬─────────────────┘
         │ (WebSocket)
         ↓
┌──────────────────────────┐
│  Server Process          │
│  - Receives line events  │
│  - Stores in memory      │
│  - Broadcasts to clients │
└────────┬─────────────────┘
         │ (Server-Sent Events)
         ↓
┌──────────────────────────┐
│  Web Browser(s)          │
│  - Lists sessions        │
│  - Shows live updates    │
│  - Auto-scrolls          │
└──────────────────────────┘
```

## Component Specifications

### 1. Watcher

**Language**: Go  
**Dependencies**: `github.com/fsnotify/fsnotify`, `github.com/gorilla/websocket`

#### Responsibilities
- Recursively scan watch directory for `.jsonl` files on startup
- Monitor directory for file creation/modification events
- Read new lines from modified files
- Batch lines within 100ms window
- Send batched lines to server via WebSocket
- Reconnect automatically on connection loss

#### Data Structures

```go
type FileState struct {
    Path      string    // Relative path from watch root
    LastLine  int       // Last line number sent
    LastSize  int64     // Last known file size
}

type LineMessage struct {
    Type  string `json:"type"`  // Always "line"
    Path  string `json:"path"`  // Relative path
    Line  string `json:"line"`  // Raw JSONL line content
}
```

#### Behavior

**Startup**:
1. Scan watch directory recursively
2. For each `.jsonl` file found:
   - Read entire file
   - Send all lines to server
   - Track final line number
3. Begin monitoring for changes

**File Modified**:
1. Detect file size changed
2. Seek to last known position
3. Read new lines to EOF
4. Add lines to batch queue
5. Update file state

**Batch Flush** (every 100ms):
1. Collect all queued lines
2. Send as array of LineMessage
3. Clear queue

**Connection Lost**:
1. Buffer lines in memory (bounded to 10,000 lines)
2. Attempt reconnect with exponential backoff (1s, 2s, 4s, 8s, max 30s)
3. On reconnect, flush buffer

#### Edge Cases

**File Truncation**: If file size < last known size, reset to beginning and resend all lines.

**File Deletion**: Log warning, remove from tracking. If recreated, treat as new file.

**Rapid Updates**: Batching handles this - multiple writes within 100ms sent as single message.

**Deep Nesting**: No depth limit, recursively watch all subdirectories.

**Symbolic Links**: Follow symlinks (fsnotify default behavior).

### 2. Server

**Language**: Go  
**Dependencies**: `github.com/gorilla/websocket`

#### Responsibilities
- Accept WebSocket connections from watchers
- Store received lines in memory
- Serve static web UI
- Stream updates to browser clients via Server-Sent Events
- Handle multiple concurrent connections

#### Data Structures

```go
type Session struct {
    Path      string      // Relative path
    Lines     []string    // All lines received
    UpdatedAt time.Time   // Last update timestamp
    mu        sync.RWMutex
}

type SessionStore struct {
    sessions map[string]*Session  // path -> session
    mu       sync.RWMutex
}

type LineEvent struct {
    Path    string `json:"path"`
    Line    string `json:"line"`
    LineNum int    `json:"line_num"`
}
```

#### API Endpoints

**WebSocket** (for watcher):
- `ws://localhost:8080/watch`
- Receives LineMessage from watcher
- No authentication (MVP)

**HTTP** (for browser):
- `GET /` - Serve index.html
- `GET /api/sessions` - List all sessions with metadata
- `GET /api/sessions/{path}` - Get full session content
- `GET /api/sessions/{path}/stream` - Server-Sent Events for live updates

#### Behavior

**Watcher Connection**:
1. Accept WebSocket connection
2. Read LineMessage in loop
3. Append line to session in memory
4. Broadcast LineEvent to all SSE clients watching that session
5. On disconnect, clean up connection (keep session data)

**Browser Connection**:
1. Client requests session list via `/api/sessions`
2. Client subscribes to session stream via SSE
3. Server sends existing lines immediately
4. Server sends new lines as they arrive
5. On disconnect, remove SSE client

**Session Management**:
- Sessions stored in memory only (MVP)
- No persistence to disk
- No garbage collection (MVP - assumes bounded session count)

#### Edge Cases

**Concurrent Writes**: Use mutex to protect session updates.

**Slow Consumer**: SSE has buffering, but if client can't keep up, disconnect them.

**Memory Growth**: Accept unbounded growth for MVP. Future: LRU cache or disk spillover.

### 3. Web UI

**Language**: HTML/JavaScript (vanilla, no framework)  
**Dependencies**: None

#### Features

**Session List**:
- Shows all active sessions
- Displays line count and last update time
- Indicates which sessions are actively updating (within last 5 seconds)
- Click session to view details

**Session Viewer**:
- Shows session path
- Displays all lines (with line numbers)
- Auto-scrolls to bottom when new lines arrive
- Syntax highlighting for JSON (optional, nice-to-have)
- Toggle auto-scroll on/off

**Live Indicator**:
- Green dot when receiving updates
- Gray dot when idle >5 seconds

#### UI Layout

```
┌─────────────────────────────────────────────────────┐
│ Agent Session Viewer                                │
├─────────────────────────────────────────────────────┤
│ Sessions                                            │
│ ┌─────────────────────────────────────────────────┐ │
│ │ ● session1.jsonl              120 lines  1s ago │ │
│ │ ○ session2.jsonl               50 lines  30s ago│ │
│ │ ● session1/subagent.jsonl      10 lines  now    │ │
│ └─────────────────────────────────────────────────┘ │
├─────────────────────────────────────────────────────┤
│ Viewing: session1.jsonl                         [⚡] │
├─────────────────────────────────────────────────────┤
│ 118  {"event":"tool_call","tool":"web_search"}     │
│ 119  {"event":"result","success":true}             │
│ 120  {"event":"thinking","content":"analyzing..."}  │
│                                                     │
│                                                     │
│ [Auto-scroll: ON]                                   │
└─────────────────────────────────────────────────────┘
```

#### Behavior

**On Load**:
1. Fetch `/api/sessions` to get session list
2. Render session list
3. Set up 5-second polling for session list refresh

**Select Session**:
1. Fetch full session content from `/api/sessions/{path}`
2. Render all lines
3. Connect to `/api/sessions/{path}/stream` for live updates
4. Display line-by-line as events arrive

**Live Updates**:
1. Receive SSE events
2. Append line to view
3. Auto-scroll to bottom (if enabled)
4. Update "last activity" indicator

**Auto-scroll**:
- Enabled by default
- Automatically disabled if user scrolls up
- Re-enabled when user scrolls to bottom

## Message Protocol

### Watcher → Server (WebSocket)

```json
{
  "type": "line",
  "path": "2026-01-04T10-00-00.jsonl",
  "line": "{\"event\":\"tool_call\",\"tool\":\"web_search\"}\n"
}
```

Note: Line includes the newline character as written to disk.

### Server → Browser (Server-Sent Events)

```
event: line
data: {"path":"2026-01-04.jsonl","line":"{\"event\":\"start\"}","line_num":1}

event: line
data: {"path":"2026-01-04.jsonl","line":"{\"event\":\"end\"}","line_num":2}
```

### Browser → Server (HTTP/JSON)

**List Sessions**:
```
GET /api/sessions

Response:
{
  "sessions": [
    {
      "path": "2026-01-04.jsonl",
      "line_count": 120,
      "updated_at": "2026-01-04T10:15:30Z"
    }
  ]
}
```

**Get Session Content**:
```
GET /api/sessions/2026-01-04.jsonl

Response:
{
  "path": "2026-01-04.jsonl",
  "lines": [
    "{\"event\":\"start\"}",
    "{\"event\":\"tool_call\"}"
  ]
}
```

## Configuration

### Watcher CLI

```bash
watcher [flags]

Flags:
  --watch <dir>       Directory to watch (required)
  --server <url>      WebSocket server URL (required)
  --batch-ms <int>    Batch interval in milliseconds (default: 100)
  --help              Show help
```

Example:
```bash
watcher --watch ~/.pi/agent/sessions --server ws://localhost:8080/watch
```

### Server CLI

```bash
server [flags]

Flags:
  --port <int>        HTTP server port (default: 8080)
  --help              Show help
```

Example:
```bash
server --port 8080
```

## Error Handling

### Watcher Errors

| Error | Behavior |
|-------|----------|
| Watch directory doesn't exist | Exit with error message |
| Cannot read file | Log warning, skip file |
| WebSocket connection fails | Retry with exponential backoff |
| WebSocket send fails | Buffer in memory, retry on reconnect |

### Server Errors

| Error | Behavior |
|-------|----------|
| Port already in use | Exit with error message |
| Invalid message from watcher | Log error, continue |
| SSE client slow/disconnected | Remove client, continue |

### Browser Errors

| Error | Behavior |
|-------|----------|
| Cannot connect to server | Show error banner, retry every 5s |
| SSE connection lost | Show "disconnected" status, retry |
| Invalid JSON in response | Log to console, show error to user |

## Performance Characteristics

### Latency Budget

| Stage | Target | Max |
|-------|--------|-----|
| File write → fsnotify detection | <10ms | 50ms |
| Watcher read + batch | <100ms | 200ms |
| WebSocket transmission | <10ms | 50ms |
| Server processing | <5ms | 20ms |
| SSE transmission | <10ms | 50ms |
| Browser rendering | <20ms | 100ms |
| **Total** | **<155ms** | **470ms** |

### Scalability Targets (MVP)

- **Files**: 1,000 concurrent .jsonl files
- **Lines**: 100,000 lines per file
- **Viewers**: 10 concurrent browser clients
- **Update Rate**: 100 lines/second total across all files
- **Memory**: <500MB for server process

## Development Phases

### Phase 1: MVP (Live Viewer)
**Scope**: Basic real-time viewing, no persistence, localhost only  
**Duration**: ~1 week  
**Deliverables**:
- Watcher that tails and streams
- Server that receives and broadcasts
- Web UI for live viewing
- Basic reconnection handling

### Phase 2: Reliability (Post-MVP)
**Scope**: State persistence, better recovery, compression  
**Additions**:
- Watcher state file (resume from last position)
- Server sync protocol (hello/sync handshake)
- Compression for bulk transfers
- Proper logging

### Phase 3: Multi-Machine (Future)
**Scope**: Support remote watchers, authentication  
**Additions**:
- API token authentication
- TLS/WSS support
- Device ID tracking
- Multi-tenant support

### Phase 4: Rich Features (Future)
**Scope**: Indexing, search, agent-specific parsing  
**Additions**:
- Database backend
- Session hierarchy parsing
- Search functionality
- Agent-type-specific UI views

## Testing Strategy

Tests will be specified in separate documents:
- `TESTS_MVP.md` - System tests for Phase 1
- `TESTS_RELIABILITY.md` - Tests for Phase 2
- `TESTS_MULTI_MACHINE.md` - Tests for Phase 3

## Open Questions

1. **Line Numbering**: Should lines be 0-indexed or 1-indexed in the API?
   - **Recommendation**: 1-indexed (matches text editor conventions)

2. **File Encoding**: How to handle non-UTF8 files?
   - **Recommendation**: MVP assumes UTF-8, display hex for invalid bytes

3. **Large Lines**: What if a single JSON line is 10MB?
   - **Recommendation**: MVP accepts any size, future can add limits

4. **Session Lifecycle**: When can we garbage collect old sessions?
   - **Recommendation**: MVP never collects, future adds LRU policy

5. **Clock Skew**: If watcher and server have different times?
   - **Recommendation**: Use watcher's timestamps, server doesn't care

## Success Criteria

### MVP Success Criteria

✅ User can start watcher pointing at agent session directory  
✅ User can open browser and see live session updates  
✅ Latency from file write to browser display is <500ms  
✅ System handles 10+ concurrent sessions without lag  
✅ Reconnection works after network interruption  
✅ No data loss during normal operation  

### User Experience Goals

- **Zero configuration**: Just point watcher at directory, open browser
- **Immediate feedback**: See agent thinking in real-time
- **Reliable**: Doesn't crash or lose data during demo
- **Intuitive**: Non-technical users can understand the UI

## References

- fsnotify: https://github.com/fsnotify/fsnotify
- gorilla/websocket: https://github.com/gorilla/websocket
- Server-Sent Events spec: https://html.spec.whatwg.org/multipage/server-sent-events.html
