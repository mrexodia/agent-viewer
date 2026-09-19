package tests

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestServerRestartWithOfflineAppends(t *testing.T) {
	env := NewTestEnv(t)
	defer env.Cleanup()
	env.CreateTestFile("session.jsonl", testDataSingle)
	env.StartServer()
	env.StartWatcher(env.testDir)
	killProcessTree(env.server)
	env.server.Wait()
	env.AppendLine("session.jsonl", `{"written":"offline"}`)
	env.StartServer()
	want := append(append([]string{}, testDataSingle...), `{"written":"offline"}`)
	if !env.WaitForLineCount("test/session.jsonl", len(want), 10*time.Second) {
		t.Fatal("offline append not restored")
	}
	if got := env.GetSessionContent("test/session.jsonl").Lines; !reflect.DeepEqual(got, want) {
		t.Fatalf("offline recovery: %q", got)
	}
}

func TestInitialScanConcurrentAppendsAreExact(t *testing.T) {
	env := NewTestEnv(t)
	defer env.Cleanup()
	env.CreateTestFile("session.jsonl", []string{`{"n":0}`})
	env.StartServer()
	done := make(chan error, 1)
	go func() {
		file, err := os.OpenFile(filepath.Join(env.testDir, "session.jsonl"), os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			done <- err
			return
		}
		defer file.Close()
		for i := 1; i <= 300; i++ {
			if _, err := fmt.Fprintf(file, "{\"n\":%d}\n", i); err != nil {
				done <- err
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
		done <- nil
	}()
	env.StartWatcher(env.testDir)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !env.WaitForLineCount("test/session.jsonl", 301, 5*time.Second) {
		t.Fatal("missing concurrent appends")
	}
	got := env.GetSessionContent("test/session.jsonl").Lines
	if len(got) != 301 {
		t.Fatalf("expected 301 lines, got %d", len(got))
	}
	for i, line := range got {
		if want := fmt.Sprintf("{\"n\":%d}", i); line != want {
			t.Fatalf("line %d: %q != %q", i, line, want)
		}
	}
}
