package main

import (
	"testing"
	"time"
)

func TestSourceIdentityAndPreviewReset(t *testing.T) {
	store := NewSessionStore(false)
	msg := SyncMessage{Type: "sync", Path: "claude/session.jsonl", Source: "claude", SourceID: "root-one", ModTime: "2020-01-02T03:04:05Z"}
	state, _ := store.Apply(msg)
	if state.Error != "" {
		t.Fatal(state.Error)
	}
	conflict := msg
	conflict.SourceID = "root-two"
	if reply, _ := store.Apply(conflict); reply.Error == "" {
		t.Fatal("same label allowed a different root to overwrite history")
	}
	msg.Type, msg.Generation = "append", state.Generation
	msg.Lines = []string{
		"{\"type\":\"user\",\"toolUseResult\":{},\"message\":{\"content\":\"skip\"}}\n",
		"{\"type\":\"user\",\"message\":{\"content\":[{\"type\":\"text\",\"text\":\"<tag>Preview</tag>\\nrest\"}]}}\n",
	}
	state, _ = store.Apply(msg)
	session := store.GetSession(msg.Path)
	if state.Error != "" || session.Source != "claude" || session.Preview != "Preview" {
		t.Fatalf("metadata: %+v / %+v", session, state)
	}
	if session.UpdatedAt.Format(time.RFC3339) != msg.ModTime {
		t.Fatal("initial history was marked live")
	}
	msg.Type, msg.Offset, msg.Lines = "reset", state.Offset, nil
	if reply, _ := store.Apply(msg); reply.Error != "" {
		t.Fatal(reply.Error)
	}
	if session.Preview != "" {
		t.Fatal("replacement retained stale preview")
	}
}
