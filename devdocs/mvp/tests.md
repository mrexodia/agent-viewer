# Agent Session Viewer - MVP Test Specification

## Test Philosophy

These are **system tests** that validate end-to-end behavior. They describe:
- **GIVEN**: Initial system state
- **WHEN**: Action or event occurs  
- **THEN**: Expected observable outcome

Tests should be **implementation-agnostic** where possible, focusing on behavior not implementation.

## Test Environment Setup

### Prerequisites
```bash
# Test directory structure
test-sessions/
├── empty/                    # Empty directory
├── single/                   # Single session file
│   └── session1.jsonl
├── multiple/                 # Multiple sessions
│   ├── session1.jsonl
│   └── session2.jsonl
└── nested/                   # Nested structure
    ├── root.jsonl
    └── root/
        └── subagent.jsonl
```

### Test Data Files

**session1.jsonl** (10 lines):
```jsonl
{"event":"start","timestamp":"2026-01-04T10:00:00Z"}
{"event":"tool_call","tool":"web_search","query":"test"}
{"event":"tool_result","success":true}
{"event":"thinking","content":"analyzing results"}
{"event":"tool_call","tool":"calculator","expr":"2+2"}
{"event":"tool_result","result":4}
{"event":"decision","action":"respond"}
{"event":"response","text":"The answer is 4"}
{"event":"end","timestamp":"2026-01-04T10:00:30Z"}
{"event":"metadata","duration_ms":30000}
```

**session2.jsonl** (5 lines):
```jsonl
{"event":"start","timestamp":"2026-01-04T09:00:00Z"}
{"event":"tool_call","tool":"file_read","path":"data.txt"}
{"event":"tool_result","content":"hello world"}
{"event":"response","text":"File contains: hello world"}
{"event":"end","timestamp":"2026-01-04T09:00:15Z"}
```

## Component Tests

### 1. Watcher Tests

#### Test 1.1: Initial Scan - Empty Directory
```
GIVEN watcher started with --watch test-sessions/empty
  AND server is running
WHEN watcher connects to server
THEN:
  - Watcher connects successfully
  - No LineMessage sent (no files to read)
  - Watcher continues monitoring directory
```

#### Test 1.2: Initial Scan - Single File
```
GIVEN test-sessions/single/ contains session1.jsonl (10 lines)
  AND server is running
WHEN watcher started with --watch test-sessions/single
THEN:
  - Watcher sends 10 LineMessage to server
  - Messages sent in correct order (line 1, 2, ..., 10)
  - Each message contains correct path: "session1.jsonl"
  - Each message contains exact line content including \n
```

**Validation**:
```bash
# Server should receive these messages:
{"type":"line","path":"session1.jsonl","line":"{\"event\":\"start\",...}\n"}
{"type":"line","path":"session1.jsonl","line":"{\"event\":\"tool_call\",...}\n"}
# ... (10 messages total)
```

#### Test 1.3: Initial Scan - Multiple Files
```
GIVEN test-sessions/multiple/ contains:
  - session1.jsonl (10 lines)
  - session2.jsonl (5 lines)
WHEN watcher started
THEN:
  - Watcher sends 15 LineMessage total
  - Lines from session1.jsonl have path="session1.jsonl"
  - Lines from session2.jsonl have path="session2.jsonl"
  - All lines from each file sent in order
```

#### Test 1.4: Initial Scan - Nested Structure
```
GIVEN test-sessions/nested/ contains:
  - root.jsonl (10 lines)
  - root/subagent.jsonl (5 lines)
WHEN watcher started with --watch test-sessions/nested
THEN:
  - Watcher sends 15 LineMessage total
  - root.jsonl lines have path="root.jsonl"
  - Subagent lines have path="root/subagent.jsonl"
```

#### Test 1.5: Live Update - Single Append
```
GIVEN watcher running and monitoring test-sessions/single
  AND session1.jsonl has 10 lines
WHEN new line appended: echo '{"event":"new"}' >> session1.jsonl
THEN within 200ms:
  - Server receives 1 LineMessage
  - Message has path="session1.jsonl"
  - Message has line='{"event":"new"}\n'
```

#### Test 1.6: Live Update - Rapid Appends
```
GIVEN watcher running with --batch-ms 100
WHEN 5 lines appended rapidly (within 50ms):
  echo '{"line":1}' >> session1.jsonl
  echo '{"line":2}' >> session1.jsonl
  echo '{"line":3}' >> session1.jsonl
  echo '{"line":4}' >> session1.jsonl
  echo '{"line":5}' >> session1.jsonl
THEN:
  - Server receives 1-2 messages (batched together)
  - All 5 lines received in correct order
  - Total latency from last append to server receipt <200ms
```

#### Test 1.7: New File Creation
```
GIVEN watcher running and monitoring test-sessions/empty
WHEN new file created:
  echo '{"event":"start"}' > test-sessions/empty/new-session.jsonl
  echo '{"event":"end"}' >> test-sessions/empty/new-session.jsonl
THEN:
  - Server receives 2 LineMessage
  - Both have path="new-session.jsonl"
  - Lines received in correct order
```

#### Test 1.8: File Truncation
```
GIVEN watcher monitoring session1.jsonl (10 lines)
  AND watcher has sent all 10 lines
WHEN file is truncated and rewritten:
  echo '{"event":"restart"}' > session1.jsonl
THEN:
  - Server receives new LineMessage with line='{"event":"restart"}\n'
  - No duplicate lines sent
```

#### Test 1.9: Subdirectory Creation
```
GIVEN watcher monitoring test-sessions/nested
WHEN new subdirectory with file created:
  mkdir -p test-sessions/nested/new-branch
  echo '{"event":"start"}' > test-sessions/nested/new-branch/agent.jsonl
THEN:
  - Server receives LineMessage
  - Message has path="new-branch/agent.jsonl"
```

#### Test 1.10: Connection Lost - Buffering
```
GIVEN watcher running and connected
WHEN server stops (connection lost)
  AND 5 lines appended to session1.jsonl
  AND server restarts
THEN:
  - Watcher reconnects automatically (within 30s)
  - All 5 buffered lines sent to server after reconnect
  - No data loss
```

#### Test 1.11: Connection Lost - Buffer Overflow
```
GIVEN watcher with buffer limit of 10,000 lines
WHEN server is down
  AND 15,000 lines appended
THEN:
  - Watcher buffers first 10,000 lines
  - Logs warning about buffer overflow
  - When reconnected, sends 10,000 buffered lines
  - Note: 5,000 lines lost (acceptable for MVP)
```

#### Test 1.12: Non-JSONL Files Ignored
```
GIVEN directory contains:
  - session.jsonl (watched)
  - readme.txt (ignored)
  - data.json (ignored)
  - config.yaml (ignored)
WHEN watcher started
THEN:
  - Only session.jsonl lines sent
  - Other files completely ignored
```

### 2. Server Tests

#### Test 2.1: Accept Watcher Connection
```
GIVEN server running on port 8080
WHEN watcher connects to ws://localhost:8080/watch
THEN:
  - WebSocket connection established
  - Server logs connection
  - Server ready to receive messages
```

#### Test 2.2: Receive and Store Lines
```
GIVEN server running
  AND watcher connected
WHEN watcher sends 10 LineMessage for "session1.jsonl"
THEN:
  - Server stores all 10 lines in memory
  - GET /api/sessions returns session1.jsonl with line_count=10
  - GET /api/sessions/session1.jsonl returns all 10 lines in order
```

#### Test 2.3: Multiple Sessions
```
GIVEN server running
WHEN watcher sends:
  - 10 lines for "session1.jsonl"
  - 5 lines for "session2.jsonl"
THEN:
  - GET /api/sessions returns 2 sessions
  - session1.jsonl has 10 lines
  - session2.jsonl has 5 lines
```

#### Test 2.4: Incremental Updates
```
GIVEN server has session1.jsonl with 10 lines
WHEN watcher sends 2 more lines for session1.jsonl
THEN:
  - GET /api/sessions shows session1.jsonl with line_count=12
  - GET /api/sessions/session1.jsonl returns all 12 lines
  - Lines 11-12 are the new lines
```

#### Test 2.5: Concurrent Watcher Connections
```
GIVEN server running
WHEN 2 watchers connect simultaneously:
  - Watcher A sends lines for "sessionA.jsonl"
  - Watcher B sends lines for "sessionB.jsonl"
THEN:
  - Both connections accepted
  - GET /api/sessions shows both sessions
  - No data mixing between sessions
```

#### Test 2.6: Watcher Disconnect
```
GIVEN server with watcher connected
  AND watcher has sent 10 lines for session1.jsonl
WHEN watcher disconnects
THEN:
  - Server logs disconnection
  - Session data remains in memory
  - GET /api/sessions still returns session1.jsonl
```

#### Test 2.7: Invalid Message Handling
```
GIVEN server running with watcher connected
WHEN watcher sends malformed JSON: "not valid json"
THEN:
  - Server logs error
  - Server does not crash
  - Server continues processing subsequent valid messages
```

### 3. Web UI Tests

#### Test 3.1: Load Empty Session List
```
GIVEN server running with no sessions
WHEN browser opens http://localhost:8080/
THEN:
  - Page loads successfully
  - Session list is empty or shows "No sessions"
  - No JavaScript errors in console
```

#### Test 3.2: Display Session List
```
GIVEN server has 2 sessions:
  - session1.jsonl (10 lines)
  - session2.jsonl (5 lines)
WHEN browser loads
THEN:
  - Session list shows both sessions
  - Each session displays:
    - Path name
    - Line count
    - Last updated time
```

#### Test 3.3: View Session Content
```
GIVEN server has session1.jsonl with 10 lines
WHEN user clicks on session1.jsonl in list
THEN:
  - Session viewer area shows all 10 lines
  - Each line has line number (1-10)
  - Lines displayed in correct order
  - JSON is readable (formatted or raw)
```

#### Test 3.4: Live Update - New Line
```
GIVEN browser viewing session1.jsonl (10 lines)
  AND SSE connection established
WHEN watcher sends new line to server
THEN within 500ms:
  - New line appears in browser
  - Line number increments correctly (11)
  - Page auto-scrolls to bottom
  - "Last updated" timestamp refreshes
```

#### Test 3.5: Live Update - Multiple Lines
```
GIVEN browser viewing session1.jsonl
WHEN watcher sends 5 lines rapidly
THEN:
  - All 5 lines appear in browser
  - Lines appear in correct order
  - No visual jank/flickering
  - Auto-scroll to latest line
```

#### Test 3.6: Live Indicator
```
GIVEN browser showing session list
WHEN session1.jsonl receives update
THEN:
  - Live indicator (dot) turns green for session1
  - After 5 seconds with no updates, indicator turns gray
```

#### Test 3.7: Auto-scroll Toggle
```
GIVEN browser viewing session with auto-scroll ON
WHEN user scrolls up manually
THEN:
  - Auto-scroll automatically disables
  - New lines still appear but no auto-scroll
  - UI shows "Auto-scroll: OFF"
WHEN user scrolls to bottom
THEN:
  - Auto-scroll automatically re-enables
  - UI shows "Auto-scroll: ON"
```

#### Test 3.8: SSE Reconnection
```
GIVEN browser viewing session with SSE connected
WHEN server restarts
THEN:
  - Browser detects SSE connection lost
  - Browser shows "Disconnected" indicator
  - Browser automatically reconnects (within 5s)
  - Live updates resume
```

#### Test 3.9: Multiple Browser Clients
```
GIVEN 2 browsers open, both viewing session1.jsonl
WHEN new line added to session
THEN:
  - Both browsers receive update
  - Both show same content
  - Updates appear at approximately same time (<100ms difference)
```

#### Test 3.10: Long Session Handling
```
GIVEN session with 10,000 lines
WHEN browser loads session
THEN:
  - Page loads without freezing
  - Lines are visible (may use virtual scrolling)
  - Scrolling is smooth
  - Live updates continue to work
```

## Integration Tests

### Test 4.1: End-to-End - Cold Start
```
GIVEN clean system (no processes running)
  AND test-sessions/single/ contains session1.jsonl (10 lines)
WHEN:
  1. Server started: server --port 8080
  2. Watcher started: watcher --watch test-sessions/single --server ws://localhost:8080/watch
  3. Browser opens http://localhost:8080/
THEN:
  - Browser shows session1.jsonl with 10 lines
  - All 10 lines visible and correct
  - Live indicator shows gray (no recent activity)
```

### Test 4.2: End-to-End - Live Session
```
GIVEN system running (server, watcher, browser all connected)
  AND browser viewing session1.jsonl
WHEN:
  1. echo '{"event":"new_1"}' >> session1.jsonl
  2. Wait 100ms
  3. echo '{"event":"new_2"}' >> session1.jsonl
THEN:
  - Both lines appear in browser within 500ms total
  - Lines appear in correct order
  - Line numbers are sequential
  - Live indicator is green
```

### Test 4.3: End-to-End - File Creation
```
GIVEN system running, browser viewing session list
WHEN:
  echo '{"event":"start"}' > test-sessions/single/new-session.jsonl
THEN within 1 second:
  - Session list updates to show new-session.jsonl
  - Session shows 1 line
  - Clicking session shows content
```

### Test 4.4: End-to-End - Rapid Agent Activity
```
GIVEN system running
WHEN script writes 100 lines rapidly (1 line per 10ms):
  for i in {1..100}; do
    echo "{\"event\":\"line_$i\"}" >> session.jsonl
    sleep 0.01
  done
THEN:
  - All 100 lines received by server
  - Browser displays all 100 lines
  - No lines lost or duplicated
  - Total latency <2 seconds (100 lines in 1 second + 1 second latency budget)
```

### Test 4.5: End-to-End - Network Interruption
```
GIVEN system running with active session
WHEN:
  1. Kill server process
  2. Append 5 lines to session
  3. Restart server
  4. Wait for watcher to reconnect
THEN:
  - Watcher reconnects automatically
  - All 5 lines sent after reconnect
  - Browser (if reconnected) shows all lines
```

### Test 4.6: End-to-End - Multiple Sessions Simultaneously
```
GIVEN system running
WHEN 3 sessions are updated simultaneously:
  echo '{"event":"A1"}' >> sessionA.jsonl &
  echo '{"event":"B1"}' >> sessionB.jsonl &
  echo '{"event":"C1"}' >> sessionC.jsonl &
  wait
THEN:
  - All 3 updates received by server
  - All 3 sessions updated correctly
  - No data mixing between sessions
```

## Performance Tests

### Test 5.1: Latency - File to Browser
```
GIVEN system running, browser viewing session
WHEN line appended with timestamp T0
  AND browser receives line at timestamp T1
THEN:
  - Latency (T1 - T0) < 500ms for 95th percentile
  - Latency (T1 - T0) < 200ms for 50th percentile
```

**Measurement**:
```bash
# Append line with timestamp
echo "{\"event\":\"test\",\"timestamp\":\"$(date -u +%s%3N)\"}" >> session.jsonl

# Browser logs receipt time
# Compare timestamps
```

### Test 5.2: Throughput - High Line Rate
```
GIVEN system running
WHEN 1000 lines written at 100 lines/second
THEN:
  - All 1000 lines received by server
  - No lines lost
  - Browser displays all lines
  - System remains responsive
```

### Test 5.3: Memory - Large Sessions
```
GIVEN 10 sessions, each with 10,000 lines (100KB per session)
WHEN all loaded in server
THEN:
  - Server memory usage < 500MB
  - Server remains responsive
  - Browser can view any session without lag
```

### Test 5.4: Concurrent Viewers
```
GIVEN 1 session receiving live updates
WHEN 10 browser clients connect and view same session
THEN:
  - All clients receive updates
  - Server handles load without degradation
  - Update latency remains <500ms for all clients
```

## Error Handling Tests

### Test 6.1: Watch Directory Doesn't Exist
```
GIVEN directory /nonexistent/path does not exist
WHEN watcher started with --watch /nonexistent/path
THEN:
  - Watcher exits with error code 1
  - Error message: "watch directory does not exist: /nonexistent/path"
```

### Test 6.2: Server Port In Use
```
GIVEN another process listening on port 8080
WHEN server started with --port 8080
THEN:
  - Server exits with error code 1
  - Error message: "failed to start server: address already in use"
```

### Test 6.3: Cannot Read File
```
GIVEN file session.jsonl exists but has no read permissions (chmod 000)
WHEN watcher scans directory
THEN:
  - Watcher logs warning: "cannot read file: session.jsonl"
  - Watcher continues monitoring other files
  - Watcher does not crash
```

### Test 6.4: Malformed JSONL
```
GIVEN session.jsonl contains invalid JSON: "not valid {json"
WHEN watcher reads and sends to server
THEN:
  - Watcher sends line as-is (watcher is dumb)
  - Server stores line as-is
  - Browser displays line as-is
  - No process crashes
```

### Test 6.5: File Deleted While Watching
```
GIVEN watcher monitoring session.jsonl
WHEN file is deleted: rm session.jsonl
THEN:
  - Watcher logs: "file deleted: session.jsonl"
  - Watcher stops tracking that file
  - Watcher continues monitoring directory
  - If file recreated, watcher treats as new file
```

### Test 6.6: Server Unreachable on Start
```
GIVEN server is not running
WHEN watcher started
THEN:
  - Watcher logs: "failed to connect to server, retrying..."
  - Watcher retries with backoff (1s, 2s, 4s, ...)
  - When server starts, watcher connects successfully
```

### Test 6.7: Browser API Fetch Fails
```
GIVEN browser open
WHEN server stops responding
THEN:
  - Browser shows error banner: "Cannot connect to server"
  - Browser retries every 5 seconds
  - When server recovers, browser auto-reconnects
```

## Acceptance Tests

### Test 7.1: User Story - First Time Setup
```
AS a developer
I WANT to monitor my agent's session
SO THAT I can see what it's doing in real-time

GIVEN I have an agent writing to ~/.pi/agent/sessions/
WHEN I:
  1. Start server in terminal 1: server
  2. Start watcher in terminal 2: watcher --watch ~/.pi/agent/sessions --server ws://localhost:8080/watch
  3. Open browser to http://localhost:8080/
THEN:
  - I see my agent's session listed
  - I can click to view it
  - I see all events in real-time
  - Setup took <2 minutes
```

### Test 7.2: User Story - Live Debugging
```
AS a developer debugging an agent
I WANT to see each action as it happens
SO THAT I can understand where it's failing

GIVEN agent is running and taking actions
  AND I'm viewing the session in browser
WHEN agent makes a tool call
THEN within 1 second:
  - I see the tool call event appear
  - I see the tool result appear
  - I see the agent's reasoning appear
  - I can scroll back to review history
```

### Test 7.3: User Story - Multiple Sessions
```
AS a developer running multiple agents
I WANT to monitor all sessions
SO THAT I can track all activity

GIVEN 3 agents running simultaneously
WHEN I open the session viewer
THEN:
  - I see all 3 sessions listed
  - I can see which are actively updating
  - I can switch between sessions
  - Each session shows correct data
```

## Test Execution Guide

### Manual Test Protocol

For each test:
1. **Setup**: Prepare test environment as described in GIVEN
2. **Execute**: Perform actions described in WHEN
3. **Verify**: Check outcomes described in THEN
4. **Cleanup**: Reset environment for next test

### Automated Test Approach

Recommend Go testing framework for integration tests:
```go
func TestEndToEnd_ColdStart(t *testing.T) {
    // Setup
    testDir := setupTestDirectory(t)
    defer cleanupTestDirectory(testDir)
    
    server := startServer(t, 8080)
    defer server.Stop()
    
    watcher := startWatcher(t, testDir, "ws://localhost:8080/watch")
    defer watcher.Stop()
    
    // Verify
    sessions := fetchSessions("http://localhost:8080/api/sessions")
    assert.Equal(t, 1, len(sessions))
    assert.Equal(t, 10, sessions[0].LineCount)
}
```

### Browser Testing

Recommend Playwright or Selenium for UI tests:
```javascript
test('displays live updates', async ({ page }) => {
  await page.goto('http://localhost:8080');
  await page.click('text=session1.jsonl');
  
  // Trigger update
  await appendLine('session1.jsonl', '{"event":"test"}');
  
  // Verify appears in browser
  await expect(page.locator('text={"event":"test"}')).toBeVisible({ timeout: 500 });
});
```

## Test Metrics

### Success Criteria

Tests must achieve:
- ✅ **100% pass rate** for all component tests (1.x, 2.x, 3.x)
- ✅ **100% pass rate** for all integration tests (4.x)
- ✅ **95% pass rate** for performance tests (5.x) - some variance acceptable
- ✅ **100% pass rate** for error handling tests (6.x)
- ✅ **100% pass rate** for acceptance tests (7.x)

### Coverage Goals

- **Watcher**: All file operations, connection handling, batching logic
- **Server**: All API endpoints, WebSocket handling, concurrency
- **Web UI**: All user interactions, SSE handling, display logic

## Notes for Implementation Agent

1. **Test First**: Run tests before writing code to ensure they fail appropriately
2. **Incremental**: Implement component by component, validating tests at each step
3. **Real Files**: Use actual file I/O in tests, not mocks (tests full stack)
4. **Timing**: Some tests have timing requirements - use generous timeouts initially
5. **Logging**: Add debug logging to help diagnose test failures
6. **Cleanup**: Ensure test cleanup happens even on failure (defer in Go, try/finally in JS)

## Test Data Generation

Helper script to generate test data:
```bash
#!/bin/bash
# generate-test-data.sh

mkdir -p test-sessions/{empty,single,multiple,nested/root}

# Single session
cat > test-sessions/single/session1.jsonl << 'EOF'
{"event":"start","timestamp":"2026-01-04T10:00:00Z"}
{"event":"tool_call","tool":"web_search","query":"test"}
{"event":"tool_result","success":true}
{"event":"thinking","content":"analyzing results"}
{"event":"tool_call","tool":"calculator","expr":"2+2"}
{"event":"tool_result","result":4}
{"event":"decision","action":"respond"}
{"event":"response","text":"The answer is 4"}
{"event":"end","timestamp":"2026-01-04T10:00:30Z"}
{"event":"metadata","duration_ms":30000}
EOF

# Multiple sessions
cp test-sessions/single/session1.jsonl test-sessions/multiple/
cat > test-sessions/multiple/session2.jsonl << 'EOF'
{"event":"start","timestamp":"2026-01-04T09:00:00Z"}
{"event":"tool_call","tool":"file_read","path":"data.txt"}
{"event":"tool_result","content":"hello world"}
{"event":"response","text":"File contains: hello world"}
{"event":"end","timestamp":"2026-01-04T09:00:15Z"}
EOF

# Nested sessions
cp test-sessions/single/session1.jsonl test-sessions/nested/root.jsonl
cat > test-sessions/nested/root/subagent.jsonl << 'EOF'
{"event":"start","timestamp":"2026-01-04T10:00:10Z"}
{"event":"tool_call","tool":"calculator","expr":"10*10"}
{"event":"tool_result","result":100}
{"event":"end","timestamp":"2026-01-04T10:00:12Z"}
EOF
```
