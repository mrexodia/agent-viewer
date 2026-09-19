package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestReadBatchKeepsOnlyCompleteRecords(t *testing.T) {
	for _, input := range []string{"", "partial", "one\npartial", "one\r\npartial", strings.Repeat("x", 5*1024*1024) + "\npartial"} {
		lines, eof, err := readBatch(bufio.NewReader(strings.NewReader(input)))
		if err != nil {
			t.Fatal(err)
		}
		got := strings.Join(lines, "")
		want := input[:strings.LastIndex(input, "\n")+1]
		if got != want {
			t.Fatalf("record boundary mismatch: got %d bytes, want %d", len(got), len(want))
		}
		if len(input) < 1024*1024 && !eof {
			t.Fatal("expected EOF")
		}
	}
}

// Fault-injecting receiver: intentionally commit a batch but lose its ACK.
// Real-server protocol behavior is covered separately in server/ and tests/.
type receiver struct {
	mu         sync.Mutex
	data       string
	generation int
	syncs      int
	dropACK    bool
	stall      bool
}

func (r *receiver) contents() string { r.mu.Lock(); defer r.mu.Unlock(); return r.data }
func (r *receiver) serve(w http.ResponseWriter, req *http.Request) {
	upgrader := websocket.Upgrader{}
	conn, err := upgrader.Upgrade(w, req, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	for {
		var msg SyncMessage
		if err := conn.ReadJSON(&msg); err != nil {
			return
		}
		r.mu.Lock()
		if r.stall {
			r.mu.Unlock()
			continue
		} // never acknowledges, but observes cancellation/close
		reply := SyncReply{Type: "ack"}
		drop := false
		switch msg.Type {
		case "ping":
			reply.Type = "pong"
		case "sync":
			r.syncs++
		case "reset":
			r.data = ""
			r.generation++
		case "append":
			if msg.Offset != int64(len(r.data)) {
				reply.Error = "incorrect append offset"
			} else {
				r.data += strings.Join(msg.Lines, "")
				drop, r.dropACK = r.dropACK, false
			}
		default:
			reply.Error = "unknown message"
		}
		reply.Generation = fmt.Sprint(r.generation)
		reply.Offset = int64(len(r.data))
		sum := sha256.Sum256([]byte(r.data))
		reply.Digest = hex.EncodeToString(sum[:])
		r.mu.Unlock()
		if drop {
			return
		}
		if err := conn.WriteJSON(reply); err != nil {
			return
		}
	}
}
func receiverServer(t *testing.T, receiver *receiver) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(receiver.serve))
	t.Cleanup(server.Close)
	return "ws" + strings.TrimPrefix(server.URL, "http")
}
func connectedWatcher(t *testing.T, receiver *receiver) *Watcher {
	t.Helper()
	w := NewWatcher([]WatchDir{{Path: t.TempDir(), Source: "test"}}, receiverServer(t, receiver), 10)
	var err error
	w.conn, _, err = websocket.DefaultDialer.Dial(w.serverURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	w.watchDirs[0].id = "test-root"
	t.Cleanup(func() { w.conn.Close() })
	return w
}

func TestReadFileRetainsPartialRecordAndDetectsReplacements(t *testing.T) {
	receiver := &receiver{}
	w := connectedWatcher(t, receiver)
	path := filepath.Join(w.watchDirs[0].Path, "session.jsonl")
	for _, step := range []struct{ contents, want string }{
		{`{"event":`, ""},
		{"{\"event\":\"done\"}\ntrailing", "{\"event\":\"done\"}\n"},
		{"{\"event\":\"done\"}\ntrailing\n", "{\"event\":\"done\"}\ntrailing\n"},
		{"short\n", "short\n"},
		{"other\n", "other\n"}, // same-size replacement
		{"a much larger replacement\n", "a much larger replacement\n"},
		{"", ""},
	} {
		if err := os.WriteFile(path, []byte(step.contents), 0600); err != nil {
			t.Fatal(err)
		}
		if err := w.readFile(path, "session.jsonl"); err != nil {
			t.Fatal(err)
		}
		if got := receiver.contents(); got != step.want {
			t.Fatalf("got %q, want %q", got, step.want)
		}
	}
}

func TestRunRecoversLostAcknowledgement(t *testing.T) {
	receiver := &receiver{dropACK: true}
	root := t.TempDir()
	var contents strings.Builder
	for i := 0; i < 400; i++ {
		fmt.Fprintf(&contents, "{\"n\":%d}\n", i)
	}
	if err := os.WriteFile(filepath.Join(root, "session.jsonl"), []byte(contents.String()), 0600); err != nil {
		t.Fatal(err)
	}
	w := NewWatcher([]WatchDir{{Path: root, Source: "test"}}, receiverServer(t, receiver), 10)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- w.RunContext(ctx) }()
	deadline := time.Now().Add(5 * time.Second)
	for receiver.contents() != contents.String() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown hung")
	}
	if got := receiver.contents(); got != contents.String() {
		t.Fatalf("recovery mismatch: got %d bytes, want %d", len(got), contents.Len())
	}
	receiver.mu.Lock()
	defer receiver.mu.Unlock()
	if receiver.syncs < 2 {
		t.Fatal("lost ACK did not trigger prefix reconciliation")
	}
}

func TestRunCancellationInterruptsUnacknowledgedIO(t *testing.T) {
	receiver := &receiver{stall: true}
	w := NewWatcher([]WatchDir{{Path: t.TempDir(), Source: "test"}}, receiverServer(t, receiver), 10)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.RunContext(ctx) }()
	time.Sleep(100 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancel did not interrupt socket read")
	}
}

func TestRunCancellationWhileServerOffline(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	url := "ws" + strings.TrimPrefix(server.URL, "http")
	server.Close()
	w := NewWatcher([]WatchDir{{Path: t.TempDir(), Source: "test"}}, url, 10)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- w.RunContext(ctx) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("reconnect loop ignored cancellation")
	}
}

func TestInvalidBatchInterval(t *testing.T) {
	if err := NewWatcher([]WatchDir{{Path: t.TempDir(), Source: "test"}}, "", 0).RunContext(context.Background()); err == nil {
		t.Fatal("accepted zero batch interval")
	}
}
