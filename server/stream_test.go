package main

import (
	"testing"
	"time"
)

func TestSlowSubscriberDoesNotBlockBroadcastOrCleanup(t *testing.T) {
	b := NewSSEBroadcaster()
	client := b.Subscribe("test")
	for i := 0; i < cap(client.events); i++ {
		b.Broadcast(LineEvent{Path: "test"})
	}
	done := make(chan struct{})
	go func() { b.Broadcast(LineEvent{Path: "test"}); b.Unsubscribe(client); close(done) }()
	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		<-client.events // release the old implementation so the test does not leak
		<-done
		t.Fatal("slow subscriber blocked broadcast and cleanup")
	}
}
