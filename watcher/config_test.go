package main

import (
	"path/filepath"
	"testing"
)

func TestWatchSpecs(t *testing.T) {
	for _, tc := range []struct{ spec, source, path string }{
		{"pi:/sessions", "pi", "/sessions"},
		{`claude:C:\sessions`, "claude", `C:\sessions`},
		{`C:\sessions`, "sessions", `C:\sessions`},
		{"C:/sessions", "sessions", "C:/sessions"},
		{"./sessions", "sessions", "./sessions"},
	} {
		got, err := parseWatchSpec(tc.spec)
		if err != nil || got.Source != tc.source || got.Path != tc.path {
			t.Fatalf("%q: %+v, %v", tc.spec, got, err)
		}
	}
	for _, spec := range []string{"", "pi:", ":/sessions", "../pi:/sessions"} {
		if _, err := parseWatchSpec(spec); err == nil {
			t.Fatalf("accepted %q", spec)
		}
	}
}

func TestWatchRootBoundariesAndNamespaces(t *testing.T) {
	root := t.TempDir()
	pi := filepath.Join(root, "sessions")
	claude := filepath.Join(pi, "claude")
	w := NewWatcher([]WatchDir{{Path: pi, Source: "pi"}, {Path: claude, Source: "claude"}}, "", 100)
	for _, tc := range []struct{ path, want, source string }{
		{filepath.Join(pi, "same.jsonl"), "pi/same.jsonl", "pi"},
		{filepath.Join(claude, "same.jsonl"), "claude/same.jsonl", "claude"},
	} {
		got, source, err := w.getRelPathAndSource(tc.path)
		if err != nil || got != tc.want || source != tc.source {
			t.Fatalf("%q: %q/%q, %v", tc.path, got, source, err)
		}
	}
	if _, _, err := w.getRelPathAndSource(filepath.Join(root, "sessions-other", "bad.jsonl")); err == nil {
		t.Fatal("accepted a sibling with a matching string prefix")
	}
}
