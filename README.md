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
go run main.go
```
Server starts at `http://localhost:7164`

### 2. Start the watcher
```bash
cd watcher
# Watch both Pi and Claude Code sessions
go run main.go --pi --claude

# Or watch a specific source
go run main.go --pi                                    # Pi sessions only
go run main.go --claude                                # Claude Code sessions only
go run main.go --watch custom:/path/to/sessions        # Custom source
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

✅ Recursive directory watching for .jsonl files
✅ Real-time streaming (<500ms latency)
✅ Multiple concurrent sessions
✅ Multiple browser viewers
✅ Auto-scroll with manual override
✅ Automatic reconnection on network issues
✅ Multi-source support (Pi and Claude Code sessions)
✅ Source-aware pretty rendering (different formats per source)

❌ No authentication (localhost only)
❌ No persistence (data lost on restart)
❌ No search/filtering  

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
event: line
data: {"path":"pi/session.jsonl","line":"{\"event\":\"new\"}","line_num":121,"source":"pi"}
```

### WebSocket Protocol (Watcher ↔ Server)

**Watcher sends**:
```json
{
  "type": "line",
  "path": "pi/session.jsonl",
  "line": "{\"event\":\"tool_call\"}\n",
  "source": "pi"
}
```

## Development

### Running Tests

See `devdocs/mvp/tests.md` for comprehensive test suite.

**Quick test**:
```bash
# Terminal 1: Start server
cd server && go run main.go

# Terminal 2: Start watcher with test data
cd watcher && go run main.go --watch ../test-sessions/single --server ws://localhost:7164/watch

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

1. **No persistence** - Restart = data loss (use syncthing or similar for backups)
2. **No auth** - Localhost only, anyone can connect
3. **In-memory only** - Large sessions consume RAM
4. **No filtering** - Shows all lines, no search
5. **No parsing** - Displays raw JSONL, no pretty formatting

These will be addressed in future phases.

## Future Enhancements

### Phase 2: Reliability
- [ ] State persistence (resume from last position)
- [ ] Sync protocol (hello/sync handshake)
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

- `devdocs/mvp/design.md` - Complete architecture and specifications
- `devdocs/mvp/tests.md` - Comprehensive test suite
- `devdocs/mvp/implementation.md` - Implementation guide
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
