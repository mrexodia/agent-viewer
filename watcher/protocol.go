package main

// Wire types for protocol v2; see server/protocol.go and devdocs/reliability.md.
type SyncMessage struct {
	Type       string   `json:"type"`
	Source     string   `json:"source,omitempty"`
	SourceID   string   `json:"source_id,omitempty"`
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
