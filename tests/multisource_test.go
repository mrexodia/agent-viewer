package tests

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestMultiSourceRestartRecoveryAndMetadata(t *testing.T) {
	env := NewTestEnv(t)
	defer env.Cleanup()
	piLines := []string{`{"type":"session","cwd":"/project"}`, `{"type":"message","message":{"role":"user","content":"Pi prompt"}}`}
	claudeLines := []string{`{"type":"user","isMeta":true,"message":{"content":"skip this"}}`, `{"type":"user","message":{"content":"<prompt>Claude prompt</prompt>\nDetails"}}`}
	piFile := env.CreateTestFile("pi-root/session.jsonl", piLines)
	claudeFile := env.CreateTestFile("claude-root/session.jsonl", claudeLines)
	oldTime := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	for _, file := range []string{piFile, claudeFile} {
		if err := os.Chtimes(file, oldTime, oldTime); err != nil {
			t.Fatal(err)
		}
	}
	specs := []string{"pi:" + filepath.Dir(piFile), "claude:" + filepath.Dir(claudeFile)}
	env.StartServer()
	env.StartWatchers(specs...)
	check := func() {
		t.Helper()
		sessions := env.GetSessions().Sessions
		if len(sessions) != 2 {
			t.Fatalf("source collision: %+v", sessions)
		}
		for _, session := range sessions {
			if session.Source != "pi" && session.Source != "claude" {
				t.Fatalf("source label lost: %+v", session)
			}
			if session.Path != session.Source+"/session.jsonl" {
				t.Fatalf("path not namespaced: %+v", session)
			}
			if session.Source == "claude" && session.Preview != "Claude prompt" {
				t.Fatalf("preview lost: %+v", session)
			}
			file, want := piFile, piLines
			if session.Source == "claude" {
				file, want = claudeFile, claudeLines
			}
			info, err := os.Stat(file)
			if err != nil {
				t.Fatal(err)
			}
			updated, err := time.Parse(time.RFC3339Nano, session.UpdatedAt)
			if err != nil || !updated.Equal(info.ModTime()) {
				t.Fatalf("ingestion replaced file timestamp: %+v, want %s", session, info.ModTime())
			}
			content := env.GetSessionContent(session.Path)
			if content.Source != session.Source || !reflect.DeepEqual(content.Lines, want) {
				t.Fatalf("incorrect source/content: %+v", content)
			}
		}
	}
	check()
	killProcessTree(env.watcher)
	env.watcher.Wait()
	env.StartWatchers(specs...)
	check() // restart must not duplicate either source or mark old files live
	piLines = append(piLines, `{"live":"pi"}`)
	claudeLines = append(claudeLines, `{"live":"claude"}`)
	env.AppendLine("pi-root/session.jsonl", piLines[len(piLines)-1])
	env.AppendLine("claude-root/session.jsonl", claudeLines[len(claudeLines)-1])
	for _, source := range []string{"pi", "claude"} {
		if !env.WaitForLineCount(source+"/session.jsonl", 3, 5*time.Second) {
			t.Fatalf("missing %s live append", source)
		}
	}
	check()
	killProcessTree(env.server)
	env.server.Wait()
	env.StartServer()
	for _, source := range []string{"pi", "claude"} {
		if !env.WaitForLineCount(source+"/session.jsonl", 3, 10*time.Second) {
			t.Fatalf("missing %s replay", source)
		}
	}
	check()
}
