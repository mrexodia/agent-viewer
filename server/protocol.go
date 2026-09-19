package main

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"log"
	"strings"
	"time"
)

// Protocol v2: offsets count original UTF-8 bytes, including terminating LF.
// Keep the wire types in sync with watcher/protocol.go.
type SyncMessage struct {
	Type       string   `json:"type"`
	Source     string   `json:"source,omitempty"`    // Human-readable source/parser label.
	SourceID   string   `json:"source_id,omitempty"` // Stable identity of the watched root.
	ModTime    string   `json:"mod_time,omitempty"`
	Path       string   `json:"path,omitempty"`
	Generation string   `json:"generation,omitempty"`
	Offset     int64    `json:"offset"`
	Lines      []string `json:"lines,omitempty"`
}

type SyncReply struct {
	Type       string `json:"type"`
	Generation string `json:"generation,omitempty"`
	Offset     int64  `json:"offset"`
	Digest     string `json:"digest,omitempty"`
	Error      string `json:"error,omitempty"`
}

func newGeneration() string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(value[:])
}

// Apply atomically validates and commits a batch before acknowledging it.
// The source files remain the durable log; the server can be rebuilt after a restart.
func (s *SessionStore) Apply(msg SyncMessage) (SyncReply, bool) {
	fail := func(reason string) (SyncReply, bool) { return SyncReply{Type: "error", Error: reason}, false }
	if msg.Type == "ping" {
		return SyncReply{Type: "pong"}, false
	}
	if msg.Type != "sync" && msg.Type != "append" && msg.Type != "reset" {
		return fail("unsupported protocol message (upgrade watcher and server together)")
	}
	if msg.Path == "" || msg.Source == "" {
		return fail("path and source required")
	}
	// Compatibility with v2 senders which used source as the root identity.
	owner := msg.SourceID
	if owner == "" {
		owner = msg.Source
	}
	modifiedAt := time.Now()
	if parsed, err := time.Parse(time.RFC3339Nano, msg.ModTime); err == nil {
		modifiedAt = parsed
	}
	s.mu.Lock()
	session := s.sessions[msg.Path]
	created := session == nil && msg.Type == "sync"
	if created {
		session = &Session{Path: msg.Path, Source: msg.Source, SourceID: owner, Generation: newGeneration(), Lines: []string{}, UpdatedAt: modifiedAt}
		s.sessions[msg.Path] = session
	}
	s.mu.Unlock()
	if session == nil {
		return fail("sync required")
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.SourceID != owner || session.Source != msg.Source {
		return fail("path belongs to another source; use distinct relative session paths")
	}
	reply := func(withDigest bool) SyncReply {
		result := SyncReply{Type: "ack", Generation: session.Generation, Offset: int64(len(session.RawContent))}
		if withDigest {
			sum := sha256.Sum256(session.RawContent)
			result.Digest = hex.EncodeToString(sum[:])
		}
		return result
	}
	if msg.Type == "sync" {
		return reply(true), created
	}
	if msg.Generation != session.Generation {
		return fail("generation changed; sync required")
	}
	if msg.Type == "reset" {
		if msg.Offset != int64(len(session.RawContent)) {
			return fail("offset changed; sync required")
		}
		session.Generation = newGeneration()
		session.Lines = []string{}
		session.RawContent = nil
		session.Preview = ""
		session.UpdatedAt = modifiedAt
		return reply(true), true
	}
	if msg.Offset < 0 || msg.Offset > int64(len(session.RawContent)) {
		return fail("offset gap; sync required")
	}
	for _, line := range msg.Lines {
		if !strings.HasSuffix(line, "\n") || strings.Count(line, "\n") != 1 {
			return fail("append requires complete individual JSONL records")
		}
	}
	data := []byte(strings.Join(msg.Lines, ""))
	if msg.Offset < int64(len(session.RawContent)) {
		// Exact retried batches are harmless, but overlapping conflicting data is not.
		remaining := session.RawContent[msg.Offset:]
		if len(data) <= len(remaining) && bytes.Equal(data, remaining[:len(data)]) {
			return reply(false), false
		}
		return fail("conflicting retry; sync required")
	}
	for _, line := range msg.Lines {
		session.Lines = append(session.Lines, strings.TrimSuffix(line, "\n"))
		if session.Source == "claude" && session.Preview == "" {
			session.Preview = claudePreview(line)
		}
	}
	session.RawContent = append(session.RawContent, data...)
	if len(data) > 0 {
		session.UpdatedAt = modifiedAt
		if s.debug {
			log.Printf("[%s] committed %d lines at byte %d", msg.Path, len(msg.Lines), len(session.RawContent))
		}
	}
	return reply(false), len(data) > 0
}
