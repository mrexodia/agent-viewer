package main

import (
	"crypto/sha256"
	"encoding/hex"
	"reflect"
	"testing"
)

func applyOK(t *testing.T, store *SessionStore, msg SyncMessage) SyncReply {
	t.Helper()
	msg.Source, msg.Path = "test-source", "session.jsonl"
	reply, _ := store.Apply(msg)
	if reply.Error != "" {
		t.Fatal(reply.Error)
	}
	return reply
}

func TestAcknowledgedBatchesAreIdempotent(t *testing.T) {
	store := NewSessionStore(false)
	state := applyOK(t, store, SyncMessage{Type: "sync"})
	msg := SyncMessage{Type: "append", Generation: state.Generation, Lines: []string{"one\n", "two\r\n"}}
	ack := applyOK(t, store, msg)
	retry := applyOK(t, store, msg) // commit succeeded but ACK was lost
	if retry.Offset != ack.Offset {
		t.Fatal("retry changed prefix")
	}
	session := store.GetSession("session.jsonl")
	if !reflect.DeepEqual(session.Lines, []string{"one", "two\r"}) {
		t.Fatalf("duplicate lines: %q", session.Lines)
	}
	synced := applyOK(t, store, SyncMessage{Type: "sync"})
	hash := sha256.Sum256([]byte("one\ntwo\r\n"))
	if synced.Digest != hex.EncodeToString(hash[:]) || synced.Offset != 9 {
		t.Fatalf("bad sync: %+v", synced)
	}
}

func TestProtocolRejectsConflictsAndStaleGenerations(t *testing.T) {
	store := NewSessionStore(false)
	state := applyOK(t, store, SyncMessage{Type: "sync"})
	ack := applyOK(t, store, SyncMessage{Type: "append", Generation: state.Generation, Lines: []string{"one\n"}})
	tests := []SyncMessage{
		{Type: "append", Generation: state.Generation, Offset: -1, Lines: []string{"x\n"}},
		{Type: "append", Generation: state.Generation, Offset: 20, Lines: []string{"x\n"}},
		{Type: "append", Generation: state.Generation, Lines: []string{"two\n"}},
		{Type: "append", Generation: state.Generation, Offset: ack.Offset, Lines: []string{"partial"}},
		{Type: "append", Generation: state.Generation, Offset: ack.Offset, Lines: []string{"a\nb\n"}},
		{Type: "reset", Generation: state.Generation, Offset: 0},
	}
	for _, msg := range tests {
		msg.Source, msg.Path = "test-source", "session.jsonl"
		if reply, changed := store.Apply(msg); reply.Error == "" || changed {
			t.Fatalf("accepted conflict: %+v", msg)
		}
	}
	if reply, _ := store.Apply(SyncMessage{Type: "sync", Source: "other-source", Path: "session.jsonl"}); reply.Error == "" {
		t.Fatal("merged unrelated sources")
	}
	reset := applyOK(t, store, SyncMessage{Type: "reset", Generation: state.Generation, Offset: ack.Offset})
	if reset.Generation == state.Generation || reset.Offset != 0 {
		t.Fatal("reset did not replace generation")
	}
	stale := SyncMessage{Type: "append", Path: "session.jsonl", Source: "test-source", Generation: state.Generation, Lines: []string{"stale\n"}}
	if reply, _ := store.Apply(stale); reply.Error == "" {
		t.Fatal("accepted stale generation")
	}
	applyOK(t, store, SyncMessage{Type: "append", Generation: reset.Generation, Lines: []string{"new\n"}})
	if got := store.GetSession("session.jsonl").Lines; !reflect.DeepEqual(got, []string{"new"}) {
		t.Fatalf("reset history: %q", got)
	}
}
