package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type sseRecord struct{ event, id, data string }

func readSSE(t *testing.T, reader *bufio.Reader) sseRecord {
	t.Helper()
	var record sseRecord
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		line = strings.TrimSuffix(line, "\n")
		if line == "" && record.event != "" {
			return record
		}
		if strings.HasPrefix(line, "event: ") {
			record.event = strings.TrimPrefix(line, "event: ")
		}
		if strings.HasPrefix(line, "id: ") {
			record.id = strings.TrimPrefix(line, "id: ")
		}
		if strings.HasPrefix(line, "data: ") {
			record.data = strings.TrimPrefix(line, "data: ")
		}
	}
}

func openSSE(t *testing.T, url, id string) (*http.Response, *bufio.Reader) {
	t.Helper()
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("Last-Event-ID", id)
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatal(resp.Status)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp, bufio.NewReader(resp.Body)
}

func TestSSESnapshotLiveResumeAndReset(t *testing.T) {
	server := NewServer(0, false)
	state := applyOK(t, server.store, SyncMessage{Type: "sync"})
	state = applyOK(t, server.store, SyncMessage{Type: "append", Generation: state.Generation, Lines: []string{"first\n"}})
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { server.handleSessionStream(w, r, "session.jsonl") }))
	defer httpServer.Close()
	resp, reader := openSSE(t, httpServer.URL, "")
	defer resp.Body.Close()
	if record := readSSE(t, reader); record.event != "reset" {
		t.Fatalf("want reset: %+v", record)
	}
	first := readSSE(t, reader)
	if first.event != "line" {
		t.Fatalf("want line: %+v", first)
	}
	// Burst much larger than the notification buffer while the client is not reading.
	var lines []string
	for i := 2; i <= 400; i++ {
		lines = append(lines, fmt.Sprintf("line-%d\n", i))
	}
	state = applyOK(t, server.store, SyncMessage{Type: "append", Generation: state.Generation, Offset: state.Offset, Lines: lines})
	for i := 0; i < 400; i++ {
		server.broadcaster.Broadcast(LineEvent{Path: "session.jsonl"})
	}
	var last sseRecord
	for i := 2; i <= 400; i++ {
		last = readSSE(t, reader)
		var event LineEvent
		if err := json.Unmarshal([]byte(last.data), &event); err != nil {
			t.Fatal(err)
		}
		if last.event != "line" || event.LineNum != i || event.Line != fmt.Sprintf("line-%d", i) || event.Source != "test-source" {
			t.Fatalf("gap/duplicate: %+v %+v", last, event)
		}
	}
	resp.Body.Close()
	// Native EventSource sends this header; only the missing suffix is replayed.
	resumed, resumedReader := openSSE(t, httpServer.URL, first.id)
	defer resumed.Body.Close()
	if record := readSSE(t, resumedReader); record.event != "line" || record.id != state.Generation+":2" {
		t.Fatalf("bad resume: %+v", record)
	}
	resumed.Body.Close()
	// Cursor at the current end also flushes immediately instead of hanging.
	current, currentReader := openSSE(t, httpServer.URL, last.id)
	defer current.Body.Close()
	state = applyOK(t, server.store, SyncMessage{Type: "reset", Generation: state.Generation, Offset: state.Offset})
	applyOK(t, server.store, SyncMessage{Type: "append", Generation: state.Generation, Lines: []string{"replacement\n"}})
	server.broadcaster.Broadcast(LineEvent{Path: "session.jsonl"})
	if record := readSSE(t, currentReader); record.event != "reset" || record.id != state.Generation+":0" {
		t.Fatalf("bad reset: %+v", record)
	}
	if record := readSSE(t, currentReader); record.event != "line" || record.id != state.Generation+":1" {
		t.Fatalf("bad replacement: %+v", record)
	}
}

func TestSSEStaleCursorAfterServerRestart(t *testing.T) {
	server := NewServer(0, false)
	state := applyOK(t, server.store, SyncMessage{Type: "sync"})
	applyOK(t, server.store, SyncMessage{Type: "append", Generation: state.Generation, Lines: []string{"new\n"}})
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { server.handleSessionStream(w, r, "session.jsonl") }))
	defer httpServer.Close()
	resp, reader := openSSE(t, httpServer.URL, "old-generation:500")
	defer resp.Body.Close()
	if record := readSSE(t, reader); record.event != "reset" {
		t.Fatalf("missing reset: %+v", record)
	}
	if record := readSSE(t, reader); record.id != state.Generation+":1" {
		t.Fatalf("stale cursor not rebuilt: %+v", record)
	}
}

func TestGlobalSSEReconcilesMetadata(t *testing.T) {
	server := NewServer(0, false)
	httpServer := httptest.NewServer(http.HandlerFunc(server.handleGlobalStream))
	defer httpServer.Close()
	resp, reader := openSSE(t, httpServer.URL, "")
	defer resp.Body.Close()
	if record := readSSE(t, reader); record.event != "sessions" || record.data != "[]" {
		t.Fatalf("initial metadata: %+v", record)
	}
	state := applyOK(t, server.store, SyncMessage{Type: "sync"})
	applyOK(t, server.store, SyncMessage{Type: "append", Generation: state.Generation, Lines: []string{"one\n", "two\n"}})
	server.broadcaster.Broadcast(LineEvent{Path: "session.jsonl"})
	record := readSSE(t, reader)
	var sessions []Session
	if err := json.Unmarshal([]byte(record.data), &sessions); err != nil {
		t.Fatal(err)
	}
	if record.event != "sessions" || len(sessions) != 1 || sessions[0].LineCount != 2 {
		t.Fatalf("metadata: %+v", record)
	}
}
