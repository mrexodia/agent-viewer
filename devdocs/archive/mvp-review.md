# Archive: mvp-review

## Goal and Scope

The MVP codebase review addressed reliability issues in the Agent Session Viewer that caused test failures on Windows and potential data loss in production. The watcher component had race conditions during startup, couldn't handle atomic file writes, dropped data when disconnected, and failed on large JSONL lines. The server lacked memory limits and had noisy debug logging. All issues were fixed using TDD, resulting in 15 passing integration tests.

## Key Architectural Decisions

1. **fsWatcher setup before initial scan** — The filesystem watcher must be active before scanning existing files, otherwise events during the scan window are lost. This was the root cause of most test failures.

2. **Dynamic line buffer (no fixed limit)** — Switched from `bufio.Scanner` (fixed max) to `bufio.Reader.ReadString()` which grows dynamically and gets garbage collected. Supports arbitrarily large JSONL lines without permanent memory allocation.

3. **Sentinel error for disconnection** — `ErrNotConnected` instead of `nil` ensures the batch sender knows to retry rather than discard data.

4. **Forward-slash path normalization** — All paths normalized to `/` regardless of OS, ensuring consistent behavior between Windows and Unix.

## Technical Insights and Gotchas

- **Windows process cleanup**: `cmd.Process.Kill()` doesn't kill child processes from `go run`. Must use `taskkill /F /T /PID` for process tree termination.

- **fsnotify Rename events**: On atomic writes (temp → rename), you get a Rename event, not Create. Must check if file exists after Rename to distinguish "renamed to" vs "renamed from".

- **bufio.Scanner token limit**: Default is 64KB, silently stops on larger lines with "token too long" error. Not obvious from the API.

- **Test isolation**: Integration tests using unique ports can still interfere if processes aren't properly killed between runs.

## API Summary

### Watcher CLI
```bash
./watcher --watch <dir> --server ws://localhost:7164/watch --batch-ms 100
```

### Server CLI  
```bash
./server --port 7164 --debug --max-lines 10000 --max-sessions 100
```

New flags added:
- `--debug`: Enable verbose logging (MD5 hashes per line)
- `--max-lines`: Limit lines per session (0 = unlimited)
- `--max-sessions`: Limit total sessions (0 = unlimited)

## Code Walkthrough

### watcher/main.go
- `normalizePath()` — Converts `\` to `/` for cross-platform consistency
- `readFile()` — Uses `bufio.Reader.ReadString('\n')` for unlimited line length
- `handleFSEvents()` — Handles Write, Create, Rename, Chmod events
- `Run()` — Sets up fsWatcher and starts event handler BEFORE initial scan
- `ErrNotConnected` — Sentinel error to prevent batch clearing on disconnect

### server/main.go
- `SessionStore` — Now has `debug`, `maxLinesPerSession`, `maxSessions` fields
- `AddLine()` — Enforces limits, evicts oldest session when full
- `handleSessionContent()` — URL decodes path, normalizes separators, rejects `..`

### tests/integration_test.go
- `Cleanup()` — Uses `taskkill /F /T` on Windows for process tree cleanup
- `TestVeryLongJSONLLine` — Verifies 5MB line handling
- `TestAtomicFileWrite` — Verifies temp→rename detection
- `TestPathTraversalRejected` — Security test for `..` in paths

## Validation

```bash
# All tests pass
make test

# Expected: 15 tests, all PASS
# Key metrics:
#   - Latency: ~100ms (target <500ms)
#   - Long lines: 5MB+ supported
#   - No zombie processes after tests
```

Manual verification:
```bash
# Terminal 1
make server

# Terminal 2  
make watcher WATCH_DIR=~/.pi/agent/sessions

# Terminal 3 - append to a session file
echo '{"test":true}' >> ~/.pi/agent/sessions/some-session.jsonl

# Browser: http://localhost:7164 should show the update
```

## References

- `devdocs/design.md` — Full system architecture
- `devdocs/pi-sessions.md` — JSONL session format
- `devdocs/archive/mvp.md` — Original MVP implementation
