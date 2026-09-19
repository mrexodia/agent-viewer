package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

type blockedWriter struct {
	header  http.Header
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (w *blockedWriter) Header() http.Header { return w.header }
func (w *blockedWriter) WriteHeader(int)     {}
func (w *blockedWriter) Flush()              {}
func (w *blockedWriter) Write(p []byte) (int, error) {
	w.once.Do(func() { close(w.entered); <-w.release })
	return len(p), nil
}

func TestBlockedSSEWriteDoesNotHoldStoreLocks(t *testing.T) {
	server := NewServer(0, false)
	state := applyOK(t, server.store, SyncMessage{Type: "sync"})
	state = applyOK(t, server.store, SyncMessage{Type: "append", Generation: state.Generation, Lines: []string{"first\n"}})
	writer := &blockedWriter{header: make(http.Header), entered: make(chan struct{}), release: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	request := httptest.NewRequest("GET", "/", nil).WithContext(ctx)
	done := make(chan struct{})
	go func() { server.handleSessionStream(writer, request, "session.jsonl"); close(done) }()
	defer func() { cancel(); close(writer.release); <-done }()
	select {
	case <-writer.entered:
	case <-time.After(time.Second):
		t.Fatal("stream did not start")
	}
	committed := make(chan SyncReply, 1)
	go func() {
		reply, _ := server.store.Apply(SyncMessage{Type: "append", Path: "session.jsonl", Source: "test-source", Generation: state.Generation, Offset: state.Offset, Lines: []string{"second\n"}})
		server.store.ListSessions()
		server.broadcaster.Broadcast(LineEvent{Path: "session.jsonl"})
		committed <- reply
	}()
	select {
	case reply := <-committed:
		if reply.Error != "" {
			t.Fatal(reply.Error)
		}
	case <-time.After(time.Second):
		t.Fatal("slow network write blocked ingestion")
	}
}
