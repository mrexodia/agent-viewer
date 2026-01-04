package tests

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// portCounter ensures each test gets a unique port
var portCounter int32 = 0

// Test fixtures
var testDataSingle = []string{
	`{"event":"start","timestamp":"2026-01-04T10:00:00Z"}`,
	`{"event":"tool_call","tool":"web_search","query":"test"}`,
	`{"event":"tool_result","success":true}`,
	`{"event":"thinking","content":"analyzing results"}`,
	`{"event":"tool_call","tool":"calculator","expr":"2+2"}`,
	`{"event":"tool_result","result":4}`,
	`{"event":"decision","action":"respond"}`,
	`{"event":"response","text":"The answer is 4"}`,
	`{"event":"end","timestamp":"2026-01-04T10:00:30Z"}`,
	`{"event":"metadata","duration_ms":30000}`,
}

var testDataSession2 = []string{
	`{"event":"start","timestamp":"2026-01-04T09:00:00Z"}`,
	`{"event":"tool_call","tool":"file_read","path":"data.txt"}`,
	`{"event":"tool_result","content":"hello world"}`,
	`{"event":"response","text":"File contains: hello world"}`,
	`{"event":"end","timestamp":"2026-01-04T09:00:15Z"}`,
}

// API response types
type SessionsResponse struct {
	Sessions []Session `json:"sessions"`
}

type Session struct {
	Path      string `json:"path"`
	LineCount int    `json:"line_count"`
	UpdatedAt string `json:"updated_at"`
}

type SessionContent struct {
	Path  string   `json:"path"`
	Lines []string `json:"lines"`
}

// TestEnv manages test server and watcher processes
type TestEnv struct {
	t          *testing.T
	projectDir string
	serverDir  string
	watcherDir string
	testDir    string
	server     *exec.Cmd
	watcher    *exec.Cmd
	port       int
}

func NewTestEnv(t *testing.T) *TestEnv {
	t.Helper()

	// Find project root by looking for server/ and watcher/ directories
	projectDir := findProjectRoot(t)

	serverDir := filepath.Join(projectDir, "server")
	watcherDir := filepath.Join(projectDir, "watcher")

	// Validate directories exist
	if _, err := os.Stat(filepath.Join(serverDir, "main.go")); err != nil {
		t.Fatalf("Cannot find server/main.go in %s: %v", projectDir, err)
	}
	if _, err := os.Stat(filepath.Join(watcherDir, "main.go")); err != nil {
		t.Fatalf("Cannot find watcher/main.go in %s: %v", projectDir, err)
	}

	// Create temp test directory
	testDir, err := os.MkdirTemp("", "agent-viewer-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}

	// Each test gets a unique port
	port := 9000 + int(atomic.AddInt32(&portCounter, 1))

	return &TestEnv{
		t:          t,
		projectDir: projectDir,
		serverDir:  serverDir,
		watcherDir: watcherDir,
		testDir:    testDir,
		port:       port,
	}
}

// findProjectRoot locates the project root by searching for server/ and watcher/ directories
func findProjectRoot(t *testing.T) string {
	t.Helper()

	// Start from current working directory
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Failed to get working directory: %v", err)
	}

	// Try current dir and parents
	dir := wd
	for i := 0; i < 5; i++ {
		serverPath := filepath.Join(dir, "server", "main.go")
		watcherPath := filepath.Join(dir, "watcher", "main.go")

		if _, err := os.Stat(serverPath); err == nil {
			if _, err := os.Stat(watcherPath); err == nil {
				return dir
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}

	t.Fatalf("Cannot find project root (looking for server/ and watcher/ directories) starting from %s", wd)
	return ""
}

func (e *TestEnv) Cleanup() {
	if e.watcher != nil && e.watcher.Process != nil {
		e.watcher.Process.Kill()
		e.watcher.Wait()
	}
	if e.server != nil && e.server.Process != nil {
		e.server.Process.Kill()
		e.server.Wait()
	}
	os.RemoveAll(e.testDir)
}

func (e *TestEnv) StartServer() {
	e.t.Helper()

	// Use 'go run' for cross-platform compatibility
	e.server = exec.Command("go", "run", ".", "--port", fmt.Sprintf("%d", e.port))
	e.server.Dir = e.serverDir
	if err := e.server.Start(); err != nil {
		e.t.Fatalf("Failed to start server: %v", err)
	}

	// Wait for server to be ready
	for i := 0; i < 50; i++ {
		resp, err := http.Get(fmt.Sprintf("http://localhost:%d/api/sessions", e.port))
		if err == nil {
			resp.Body.Close()
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	e.t.Fatal("Server failed to start within 5 seconds")
}

func (e *TestEnv) StartWatcher(watchDir string) {
	e.t.Helper()

	// Use 'go run' for cross-platform compatibility
	serverURL := fmt.Sprintf("ws://localhost:%d/watch", e.port)
	e.watcher = exec.Command("go", "run", ".", "--watch", watchDir, "--server", serverURL)
	e.watcher.Dir = e.watcherDir
	if err := e.watcher.Start(); err != nil {
		e.t.Fatalf("Failed to start watcher: %v", err)
	}
	// Give watcher time to connect and scan
	time.Sleep(500 * time.Millisecond)
}

func (e *TestEnv) CreateTestFile(relPath string, lines []string) string {
	e.t.Helper()
	fullPath := filepath.Join(e.testDir, relPath)
	dir := filepath.Dir(fullPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		e.t.Fatalf("Failed to create dir %s: %v", dir, err)
	}

	content := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(fullPath, []byte(content), 0644); err != nil {
		e.t.Fatalf("Failed to write file %s: %v", fullPath, err)
	}
	return fullPath
}

func (e *TestEnv) AppendLine(relPath, line string) {
	e.t.Helper()
	fullPath := filepath.Join(e.testDir, relPath)
	f, err := os.OpenFile(fullPath, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		e.t.Fatalf("Failed to open file for append: %v", err)
	}
	defer f.Close()
	if _, err := f.WriteString(line + "\n"); err != nil {
		e.t.Fatalf("Failed to append line: %v", err)
	}
}

func (e *TestEnv) GetSessions() SessionsResponse {
	e.t.Helper()
	resp, err := http.Get(fmt.Sprintf("http://localhost:%d/api/sessions", e.port))
	if err != nil {
		e.t.Fatalf("Failed to get sessions: %v", err)
	}
	defer resp.Body.Close()

	var result SessionsResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		e.t.Fatalf("Failed to decode sessions: %v", err)
	}
	return result
}

func (e *TestEnv) GetSessionContent(path string) SessionContent {
	e.t.Helper()
	resp, err := http.Get(fmt.Sprintf("http://localhost:%d/api/sessions/%s", e.port, path))
	if err != nil {
		e.t.Fatalf("Failed to get session content: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		e.t.Fatalf("Failed to get session content: %s", body)
	}

	var result SessionContent
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		e.t.Fatalf("Failed to decode session content: %v", err)
	}
	return result
}

func (e *TestEnv) WaitForLineCount(path string, expectedCount int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		sessions := e.GetSessions()
		for _, s := range sessions.Sessions {
			if s.Path == path && s.LineCount >= expectedCount {
				return true
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

func (e *TestEnv) WaitForSessionCount(expectedCount int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		sessions := e.GetSessions()
		if len(sessions.Sessions) >= expectedCount {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

// === TESTS ===

func TestInitialScan_SingleFile(t *testing.T) {
	env := NewTestEnv(t)
	defer env.Cleanup()

	// Setup: Create session with 10 lines
	env.CreateTestFile("session1.jsonl", testDataSingle)

	// Start system
	env.StartServer()
	env.StartWatcher(env.testDir)

	// Wait and verify
	if !env.WaitForLineCount("session1.jsonl", 10, 2*time.Second) {
		t.Fatal("Expected 10 lines within 2 seconds")
	}

	sessions := env.GetSessions()
	if len(sessions.Sessions) != 1 {
		t.Fatalf("Expected 1 session, got %d", len(sessions.Sessions))
	}
	if sessions.Sessions[0].LineCount != 10 {
		t.Fatalf("Expected 10 lines, got %d", sessions.Sessions[0].LineCount)
	}
}

func TestInitialScan_MultipleFiles(t *testing.T) {
	env := NewTestEnv(t)
	defer env.Cleanup()

	// Setup: Create 2 sessions
	env.CreateTestFile("session1.jsonl", testDataSingle)
	env.CreateTestFile("session2.jsonl", testDataSession2)

	// Start system
	env.StartServer()
	env.StartWatcher(env.testDir)

	// Wait and verify
	if !env.WaitForSessionCount(2, 2*time.Second) {
		t.Fatal("Expected 2 sessions within 2 seconds")
	}

	sessions := env.GetSessions()
	if len(sessions.Sessions) != 2 {
		t.Fatalf("Expected 2 sessions, got %d", len(sessions.Sessions))
	}

	totalLines := 0
	for _, s := range sessions.Sessions {
		totalLines += s.LineCount
	}
	if totalLines != 15 {
		t.Fatalf("Expected 15 total lines, got %d", totalLines)
	}
}

func TestInitialScan_NestedStructure(t *testing.T) {
	env := NewTestEnv(t)
	defer env.Cleanup()

	// Setup: Create nested structure
	env.CreateTestFile("root.jsonl", testDataSingle)
	env.CreateTestFile("subdir/subagent.jsonl", testDataSession2)

	// Start system
	env.StartServer()
	env.StartWatcher(env.testDir)

	// Wait and verify
	if !env.WaitForSessionCount(2, 2*time.Second) {
		t.Fatal("Expected 2 sessions within 2 seconds")
	}

	sessions := env.GetSessions()
	hasNested := false
	for _, s := range sessions.Sessions {
		if strings.Contains(s.Path, "subdir/") {
			hasNested = true
			break
		}
	}
	if !hasNested {
		t.Fatal("Expected nested path subdir/subagent.jsonl")
	}
}

func TestLiveUpdate_SingleAppend(t *testing.T) {
	env := NewTestEnv(t)
	defer env.Cleanup()

	// Setup
	env.CreateTestFile("session1.jsonl", testDataSingle)

	// Start system
	env.StartServer()
	env.StartWatcher(env.testDir)

	// Wait for initial scan
	if !env.WaitForLineCount("session1.jsonl", 10, 2*time.Second) {
		t.Fatal("Initial scan failed")
	}

	// Append new line
	env.AppendLine("session1.jsonl", `{"event":"live_test"}`)

	// Verify update received
	if !env.WaitForLineCount("session1.jsonl", 11, 500*time.Millisecond) {
		t.Fatal("Live update not received within 500ms")
	}
}

func TestLiveUpdate_RapidAppends(t *testing.T) {
	env := NewTestEnv(t)
	defer env.Cleanup()

	// Setup
	env.CreateTestFile("session1.jsonl", testDataSingle)

	// Start system
	env.StartServer()
	env.StartWatcher(env.testDir)

	// Wait for initial scan
	if !env.WaitForLineCount("session1.jsonl", 10, 2*time.Second) {
		t.Fatal("Initial scan failed")
	}

	// Append 5 lines rapidly
	for i := 1; i <= 5; i++ {
		env.AppendLine("session1.jsonl", fmt.Sprintf(`{"event":"rapid_%d"}`, i))
	}

	// Verify all updates received
	if !env.WaitForLineCount("session1.jsonl", 15, 1*time.Second) {
		sessions := env.GetSessions()
		t.Fatalf("Expected 15 lines, got %d", sessions.Sessions[0].LineCount)
	}
}

func TestNewFileCreation(t *testing.T) {
	env := NewTestEnv(t)
	defer env.Cleanup()

	// Setup: empty directory
	os.MkdirAll(env.testDir, 0755)

	// Start system
	env.StartServer()
	env.StartWatcher(env.testDir)

	// Verify empty
	sessions := env.GetSessions()
	if len(sessions.Sessions) != 0 {
		t.Fatalf("Expected 0 sessions initially, got %d", len(sessions.Sessions))
	}

	// Create new file
	env.CreateTestFile("new-session.jsonl", []string{`{"event":"new"}`})

	// Verify detected
	if !env.WaitForSessionCount(1, 1*time.Second) {
		t.Fatal("New file not detected within 1 second")
	}
}

func TestSessionContent_Correct(t *testing.T) {
	env := NewTestEnv(t)
	defer env.Cleanup()

	// Setup
	env.CreateTestFile("session1.jsonl", testDataSingle)

	// Start system
	env.StartServer()
	env.StartWatcher(env.testDir)

	// Wait for initial scan
	if !env.WaitForLineCount("session1.jsonl", 10, 2*time.Second) {
		t.Fatal("Initial scan failed")
	}

	// Get content and verify
	content := env.GetSessionContent("session1.jsonl")
	if len(content.Lines) != 10 {
		t.Fatalf("Expected 10 lines, got %d", len(content.Lines))
	}

	// Verify first line
	if !strings.Contains(content.Lines[0], `"event":"start"`) {
		t.Fatalf("First line should contain start event, got: %s", content.Lines[0])
	}

	// Verify last line
	if !strings.Contains(content.Lines[9], `"duration_ms"`) {
		t.Fatalf("Last line should contain duration_ms, got: %s", content.Lines[9])
	}
}

func TestLatency_FileToServer(t *testing.T) {
	env := NewTestEnv(t)
	defer env.Cleanup()

	// Setup
	env.CreateTestFile("session1.jsonl", testDataSingle)

	// Start system
	env.StartServer()
	env.StartWatcher(env.testDir)

	// Wait for initial scan
	if !env.WaitForLineCount("session1.jsonl", 10, 2*time.Second) {
		t.Fatal("Initial scan failed")
	}

	// Measure latency
	start := time.Now()
	env.AppendLine("session1.jsonl", `{"event":"latency_test"}`)

	if !env.WaitForLineCount("session1.jsonl", 11, 500*time.Millisecond) {
		t.Fatal("Update not received within 500ms")
	}
	latency := time.Since(start)

	t.Logf("Latency: %v", latency)
	if latency > 500*time.Millisecond {
		t.Fatalf("Latency too high: %v (target <500ms)", latency)
	}
}

func TestEmptyDirectory(t *testing.T) {
	env := NewTestEnv(t)
	defer env.Cleanup()

	// Setup: empty directory
	os.MkdirAll(env.testDir, 0755)

	// Start system
	env.StartServer()
	env.StartWatcher(env.testDir)

	// Verify empty
	sessions := env.GetSessions()
	if len(sessions.Sessions) != 0 {
		t.Fatalf("Expected 0 sessions, got %d", len(sessions.Sessions))
	}
}

func TestNestedDirectoryCreation(t *testing.T) {
	env := NewTestEnv(t)
	defer env.Cleanup()

	// Setup: empty directory
	os.MkdirAll(env.testDir, 0755)

	// Start system
	env.StartServer()
	env.StartWatcher(env.testDir)

	// Verify empty
	sessions := env.GetSessions()
	if len(sessions.Sessions) != 0 {
		t.Fatalf("Expected 0 sessions initially, got %d", len(sessions.Sessions))
	}

	// Create nested directory structure (like pi does)
	nestedDir := filepath.Join(env.testDir, "sessions", "project-name")
	if err := os.MkdirAll(nestedDir, 0755); err != nil {
		t.Fatalf("Failed to create nested dir: %v", err)
	}

	// Give watcher time to detect the new directories
	time.Sleep(200 * time.Millisecond)

	// Create .jsonl file in the nested directory
	jsonlPath := filepath.Join(nestedDir, "session.jsonl")
	if err := os.WriteFile(jsonlPath, []byte(`{"event":"start"}`+"\n"), 0644); err != nil {
		t.Fatalf("Failed to create jsonl file: %v", err)
	}

	// Verify detected
	if !env.WaitForSessionCount(1, 2*time.Second) {
		sessions := env.GetSessions()
		t.Fatalf("Expected 1 session, got %d", len(sessions.Sessions))
	}

	// Verify path is correct
	sessions = env.GetSessions()
	expectedPath := "sessions/project-name/session.jsonl"
	if sessions.Sessions[0].Path != expectedPath {
		t.Fatalf("Expected path %s, got %s", expectedPath, sessions.Sessions[0].Path)
	}
}

func TestNonJSONLFilesIgnored(t *testing.T) {
	env := NewTestEnv(t)
	defer env.Cleanup()

	// Setup: Create mixed files
	env.CreateTestFile("session.jsonl", testDataSingle)
	env.CreateTestFile("readme.txt", []string{"This is a readme"})
	env.CreateTestFile("data.json", []string{`{"key": "value"}`})

	// Start system
	env.StartServer()
	env.StartWatcher(env.testDir)

	// Wait for scan
	time.Sleep(500 * time.Millisecond)

	// Verify only .jsonl file picked up
	sessions := env.GetSessions()
	if len(sessions.Sessions) != 1 {
		t.Fatalf("Expected 1 session, got %d", len(sessions.Sessions))
	}
	if sessions.Sessions[0].Path != "session.jsonl" {
		t.Fatalf("Expected session.jsonl, got %s", sessions.Sessions[0].Path)
	}
}
