# Agent Session Viewer

Real-time monitoring for agent workflows that write JSONL event logs.

## What is this?

A lightweight system to watch agent sessions as they happen, with <500ms latency from file write to browser display.

**Use cases**:
- Debug agents in real-time
- Monitor multiple concurrent agents
- Review historical sessions
- Demo agent workflows live

## Quick Start

### 1. Start the server
```bash
cd server
go run .
```
Server starts at `http://localhost:7164`

### 2. Start the watcher
```bash
cd watcher
# Watch both Pi and Claude Code sessions
go run . --pi --claude

# Or watch a specific source
go run . --pi                                    # Pi sessions only
go run . --claude                                # Claude Code sessions only
go run . --watch custom:/path/to/sessions         # Custom source
```

### 3. Open your browser
Navigate to `http://localhost:7164` and watch your agent sessions live!

## Architecture

```
Agent writes       Watcher tails      Server receives    Browser displays
session.jsonl  →   & streams      →   & broadcasts   →   live in HTML
                   (WebSocket)         (SSE)
```

**Three components**:
1. **Watcher** - Monitors local .jsonl files, streams updates to server
2. **Server** - Receives updates, stores in memory, broadcasts to browsers
3. **Web UI** - Displays sessions with live updates

## Features (MVP)

- ✅ Recursive directory watching for .jsonl files
- ✅ Real-time streaming (<500ms latency target)
- ✅ Multiple concurrent sessions and browser viewers
- ✅ Auto-scroll with manual override
- ✅ Acknowledged batches and prefix reconciliation after disconnects/restarts
- ✅ Complete-record handling and file replacement detection
- ✅ Cursor-based browser replay without slow-viewer backpressure
- ✅ Multi-source support for Pi, Claude Code, and custom watch directories
- ✅ Raw and source-aware pretty views, including tool results and usage
- ❌ No authentication (the server currently binds all interfaces)
- ❌ No server-side persistence (history is rebuilt from available watched files)
- ❌ No search/filtering

Upgrade watcher and server together: the acknowledged protocol replaces the original fire-and-forget `line` messages. See [recovery and streaming semantics](devdocs/reliability.md).

## Usage

### Watcher CLI

```bash
watcher [flags]

Flags:
  --pi                Watch Pi sessions at ~/.pi/agent/sessions
  --claude            Watch Claude Code sessions at ~/.claude/projects
  --watch <source:path>  Custom directory to watch (format: source:path)
  --server <url>      WebSocket server URL (default: ws://localhost:7164/watch)
  --batch-ms <int>    Batch interval in milliseconds (default: 100)
  --help              Show help
```

**Examples**:
```bash
# Watch both Pi and Claude Code sessions
watcher --pi --claude

# Watch Pi sessions only
watcher --pi

# Watch Claude Code sessions only
watcher --claude

# Watch a custom directory with a named source
watcher --watch custom:~/agent-sessions

# Watch multiple custom directories
watcher --watch source1:/path/one --watch source2:/path/two
```

### Server CLI

```bash
server [flags]

Flags:
  --port <int>        HTTP server port (default: 7164)
  --help              Show help
```

**Example**:
```bash
server --port 9000
```

## File Structure

```
agent-viewer/
├── watcher/
│   ├── main.go              # Watcher daemon
│   └── go.mod
├── server/
│   ├── main.go              # Server + API
│   ├── go.mod
│   └── static/
│       └── index.html       # Web UI
└── test-sessions/           # Test data
```

## API Reference

### REST API

**List sessions**:
```
GET /api/sessions

Response:
{
  "sessions": [
    {
      "path": "pi/session.jsonl",
      "line_count": 120,
      "updated_at": "2026-01-04T10:15:30Z",
      "source": "pi"
    },
    {
      "path": "claude/-Users-project/session-uuid.jsonl",
      "line_count": 50,
      "updated_at": "2026-01-04T11:00:00Z",
      "source": "claude"
    }
  ]
}
```

**Get session content**:
```
GET /api/sessions/{path}

Response:
{
  "path": "pi/session.jsonl",
  "source": "pi",
  "lines": [
    "{\"event\":\"start\"}",
    "{\"event\":\"tool_call\"}"
  ]
}
```

**Stream session updates** (Server-Sent Events):
```
GET /api/sessions/{path}/stream

Response (SSE):
id: <generation>:121
event: line
data: {"path":"pi/session.jsonl","line":"{\"event\":\"new\"}","line_num":121,"source":"pi"}
```

### WebSocket Protocol (Watcher ↔ Server)

The watcher first sends `sync` with a stable source ID and relative path. The server returns a generation, committed byte offset and SHA-256 prefix digest. Matching prefixes resume; mismatches use a conditional `reset` before replay.

**Watcher sends a batch**:
```json
{
  "type": "append",
  "source": "pi",
  "source_id": "<stable-watch-root-id>",
  "path": "pi/session.jsonl",
  "mod_time": "2026-01-04T10:15:30Z",
  "generation": "<generation-from-sync>",
  "offset": 0,
  "lines": ["{\"event\":\"tool_call\"}\n"]
}
```

Every batch is acknowledged after commit. Offsets include original terminating newline bytes. Only complete newline-terminated records are ingested. See `devdocs/reliability.md` for ACKs, reset behavior, and SSE resume semantics.

## Development

### Running Tests

Run `make test` for server/watcher unit tests and end-to-end integration tests. Run `make test-ui` for UI stream-state tests (Node.js 18+, no npm dependencies). See `devdocs/reliability.md` for regression coverage.

**Quick test**:
```bash
# Terminal 1: Start server
cd server && go run .

# Terminal 2: Start watcher with test data
cd watcher && go run . --watch ../test-sessions/single --server ws://localhost:7164/watch

# Terminal 3: Append to test file
echo '{"event":"test"}' >> test-sessions/single/session1.jsonl

# Browser: Open http://localhost:7164 and see the update appear!
```

### Test Data

Generate test data:
```bash
mkdir -p test-sessions/single
cat > test-sessions/single/session1.jsonl << 'EOF'
{"event":"start","timestamp":"2026-01-04T10:00:00Z"}
{"event":"tool_call","tool":"web_search"}
{"event":"tool_result","success":true}
{"event":"end","timestamp":"2026-01-04T10:00:30Z"}
EOF
```

## Troubleshooting

### Watcher won't connect
- Check server is running: `curl http://localhost:7164/api/sessions`
- Check WebSocket URL is correct (should start with `ws://` not `http://`)
- Check firewall/network settings

### Browser shows no sessions
- Check watcher is running and connected
- Check watch directory has .jsonl files
- Open browser console for errors
- Check `/api/sessions` returns data

### Updates are slow
- Expected latency: <500ms
- Check system load (CPU/memory)
- Check network latency (if server is remote)
- Verify batch interval: lower = faster but more CPU

### Memory usage high
- Server stores all lines in memory
- Expected: ~1KB per line
- 100k lines = ~100MB memory
- Future versions will add disk storage

## Performance

| Metric | Target |
|--------|--------|
| Latency (file write → browser) | <500ms |
| Concurrent sessions | 100+ |
| Lines per session | 100,000+ |
| Concurrent viewers | 10+ |
| Memory (10 sessions @ 10k lines) | <500MB |

## Limitations (MVP)

1. **No server persistence** - Restarts require a connected watcher and the original files to rebuild history. Keep those files until recovery completes.
2. **No auth** - Binding/origin behavior is unchanged; do not expose this server to untrusted networks
3. **In-memory only** - Large sessions consume RAM
4. **No filtering** - Shows all lines, no search
5. **Partial parsing** - Pretty view supports common Pi events; full branch/context interpretation is not implemented

These will be addressed in future phases.

## Future Enhancements

### Phase 2: Reliability
- [x] Resume from the acknowledged server prefix
- [x] Sync/reset protocol with idempotent retries
- [x] Browser cursors and nonblocking history catch-up
- [ ] Optional durable server storage
- [ ] Compression for bulk transfers
- [ ] Better logging

### Phase 3: Multi-Machine
- [ ] API token authentication
- [ ] TLS/WSS support
- [ ] Remote watcher connections
- [ ] Device tracking

### Phase 4: Rich Features
- [ ] Database backend (PostgreSQL)
- [ ] Full-text search
- [ ] Session hierarchy visualization
- [ ] Agent-specific parsing (pi, custom, etc.)
- [ ] Export/archive functionality

## Documentation

- `devdocs/design.md` - Original architecture and specifications
- `devdocs/reliability.md` - Current ingestion/recovery protocol, streaming, and validation
- `devdocs/archive/mvp.md` - Original implementation history
- `README.md` - This file

## Contributing

This is currently an MVP. Focus areas:
1. Test coverage - ensure all tests pass
2. Performance - measure and optimize latency
3. Error handling - graceful failures
4. Documentation - keep docs updated

## License

[TBD]

## Support

Issues? Questions?
- Check documentation in this repo
- Review test suite for examples
- Check browser console for errors
- Enable debug logging in watcher/server

---

**Status**: MVP - Real-time viewing works, persistence and advanced features coming soon!
