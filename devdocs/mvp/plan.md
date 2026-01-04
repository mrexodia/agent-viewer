# MVP Implementation - Plan

## Epic Contents

| File | Purpose |
|------|---------|
| [design.md](design.md) | Full architecture, protocols, edge cases |
| [implementation.md](implementation.md) | Code patterns, snippets, implementation order |
| [tests.md](tests.md) | Comprehensive test specifications |
| plan.md | This file - phase tracking |

## Phases

### Phase 1: Server Foundation
- Create basic HTTP server
- Serve static HTML page
- Implement `/api/sessions` endpoint (return empty array)
- **Validation**: Can load page in browser

### Phase 2: Server WebSocket
- Add WebSocket handler at `/watch`
- Implement in-memory session storage
- Store received lines in map[path][]string
- Update `/api/sessions` to return actual data
- **Validation**: Can send WebSocket message, see in API response

### Phase 3: Watcher Core
- Implement directory scanning
- Read .jsonl files line-by-line
- Connect to server WebSocket
- Send all lines on startup
- **Validation**: Watcher sends existing files to server

### Phase 4: Live Updates
- Add fsnotify to watcher
- Detect file modifications
- Read new lines and send to server
- Implement 100ms batching
- **Validation**: File append triggers update

### Phase 5: Browser Live View
- Add SSE endpoint to server
- Broadcast new lines to SSE clients
- Implement SSE client in HTML
- Update UI when lines received
- **Validation**: Browser shows live updates

### Phase 6: Polish
- Add auto-scroll logic
- Add live indicators
- Add error handling
- Add reconnection logic
- **Validation**: Full end-to-end scenarios work

### Phase 7: Testing
- Run all tests from [tests.md](tests.md)
- Fix any failures
- Measure performance
- Document any deviations from spec
- **Validation**: All tests pass, <500ms latency confirmed

## Current Status

Not started - epic created for tracking.
