package tests

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestPartialJSONLWrite(t *testing.T) {
	env := NewTestEnv(t)
	defer env.Cleanup()
	env.CreateTestFile("ready.jsonl", []string{`{"ready":true}`})
	env.StartServer()
	env.StartWatcher(env.testDir)
	if !env.WaitForLineCount("test/ready.jsonl", 1, 5*time.Second) {
		t.Fatal("not ready")
	}
	path := filepath.Join(env.testDir, "partial.jsonl")
	if err := os.WriteFile(path, []byte(`{"event":`), 0600); err != nil {
		t.Fatal(err)
	}
	// Give the filesystem event a chance to expose the incomplete record.
	time.Sleep(400 * time.Millisecond)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteString("\"complete\"}\n")
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	if !env.WaitForLineCount("test/partial.jsonl", 1, 5*time.Second) {
		t.Fatal("no completed line")
	}
	if got := env.GetSessionContent("test/partial.jsonl").Lines; !reflect.DeepEqual(got, []string{`{"event":"complete"}`}) {
		t.Fatalf("partial write corrupted: %q", got)
	}
}

func TestWatcherRestartDoesNotDuplicateHistory(t *testing.T) {
	env := NewTestEnv(t)
	defer env.Cleanup()
	env.CreateTestFile("session.jsonl", testDataSingle)
	env.StartServer()
	env.StartWatcher(env.testDir)
	if !env.WaitForLineCount("test/session.jsonl", len(testDataSingle), 5*time.Second) {
		t.Fatal("not ready")
	}
	killProcessTree(env.watcher)
	env.watcher.Wait()
	env.StartWatcher(env.testDir)
	env.AppendLine("session.jsonl", `{"after":"restart"}`)
	if !env.WaitForLineCount("test/session.jsonl", len(testDataSingle)+1, 5*time.Second) {
		t.Fatal("no update")
	}
	want := append(append([]string{}, testDataSingle...), `{"after":"restart"}`)
	if got := env.GetSessionContent("test/session.jsonl").Lines; !reflect.DeepEqual(got, want) {
		t.Fatalf("restart history: got %q, want %q", got, want)
	}
}

func TestServerRestartRebuildsIdleHistory(t *testing.T) {
	env := NewTestEnv(t)
	defer env.Cleanup()
	env.CreateTestFile("session.jsonl", testDataSingle)
	env.StartServer()
	env.StartWatcher(env.testDir)
	if !env.WaitForLineCount("test/session.jsonl", len(testDataSingle), 5*time.Second) {
		t.Fatal("not ready")
	}
	killProcessTree(env.server)
	env.server.Wait()
	env.StartServer()
	if !env.WaitForLineCount("test/session.jsonl", len(testDataSingle), 10*time.Second) {
		t.Fatal("idle history not restored after server restart")
	}
	if got := env.GetSessionContent("test/session.jsonl").Lines; !reflect.DeepEqual(got, testDataSingle) {
		t.Fatalf("history mismatch: %q", got)
	}
}

func TestFileReplacementResetsHistory(t *testing.T) {
	env := NewTestEnv(t)
	defer env.Cleanup()
	env.CreateTestFile("session.jsonl", testDataSingle)
	env.StartServer()
	env.StartWatcher(env.testDir)
	if !env.WaitForLineCount("test/session.jsonl", len(testDataSingle), 5*time.Second) {
		t.Fatal("not ready")
	}
	want := []string{`{"replacement":true}`}
	env.CreateTestFile("session.jsonl", want)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if reflect.DeepEqual(env.GetSessionContent("test/session.jsonl").Lines, want) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("replacement appended to old generation: %q", env.GetSessionContent("test/session.jsonl").Lines)
}
