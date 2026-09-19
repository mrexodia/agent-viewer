# Agent Session Viewer - Project Overview

Real-time monitoring for agent workflows that write JSONL event logs.

## Architecture

```
Agent writes       Watcher tails      Server receives    Browser displays
session.jsonl  →   & streams      →   & broadcasts   →   live in HTML
                   (WebSocket)         (SSE)
```

**Three components**:
| Component | Language | Purpose |
|-----------|----------|---------|
| Watcher | Go | Monitor .jsonl files, stream to server |
| Server | Go | Receive updates, serve UI, broadcast to browsers |
| Web UI | HTML/JS | Display sessions with live updates |

## File Structure (Target)

```
agent-viewer/
├── watcher/
│   ├── main.go
│   └── go.mod
├── server/
│   ├── main.go
│   ├── go.mod
│   └── static/
│       └── index.html
└── test-sessions/
```

## Development Commands

```bash
# Start server (port 7164)
cd server && go run .

# Start watcher
cd watcher && go run . --watch ~/.pi/agent/sessions --server ws://localhost:7164/watch

# Test manually
echo '{"event":"test"}' >> test-sessions/single/session1.jsonl
```

## Key Docs

| Document | Purpose |
|----------|---------|
| devdocs/design.md | Original architecture and design intent |
| devdocs/reliability.md | Current recovery/streaming protocol and regression tests |
| devdocs/archive/mvp.md | Original implementation history |
| README.md | User-facing documentation |

## Performance Targets

- Latency (file write → browser): <500ms
- Concurrent sessions: 100+
- Lines per session: 100,000+
- Memory (10 sessions @ 10k lines): <500MB
