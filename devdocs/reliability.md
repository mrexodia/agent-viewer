# Reliable ingestion and live viewing

## Contract and scope

The watched JSONL files are the durable source of truth. The server remains an
in-memory mirror. Complete LF-terminated UTF-8 records are delivered in file order;
partial trailing records remain pending on disk. The watcher and server can restart
independently without duplicating history, provided the source files remain available.
Replacement/truncation replaces the mirrored session rather than appending to it.

Network binding, authentication, origin checks, and log-field HTML escaping were
not changed by this reliability work. Do not expose the server to untrusted networks.

## Watcher ownership and recovery

`watcher/main.go` has one owner loop for scanning, filesystem events and WebSocket
request/ACK exchanges. Initial scan and filesystem events cannot read the same file
concurrently. `--batch-ms` controls event coalescing (default 100ms, must be positive).
A one-second rescan provides fallback coverage for missed filesystem events and new
directories. A one-second ping exchange detects an idle server restart.

Batches contain at most 128 records or approximately 1 MiB. A single large record
can exceed that budget; there is no fixed Scanner token limit. Reads are bounded by
the file size observed at the start of a pass so continuously growing files do not
starve other sessions. A missing final LF never causes an invented newline.

The watcher accepts `--pi`, `--claude`, and repeated `--watch source:path` arguments.
Plain paths use their basename as the label; Windows drive letters are not treated
as source labels. Each watch root needs a distinct label. API paths are namespaced
as `<source>/<relative-path>`, so Pi and Claude files with identical names coexist.
Nested roots use the most specific matching directory, with path-component boundary
checks. Missing roots are skipped with a warning; at least one must exist.

`source` is the human-readable/parser label (`pi`, `claude`, etc.). Separately,
`source_id` hashes hostname plus the canonical absolute watch directory. A different
root cannot overwrite an existing session just by reusing its label/path. Moving
the root changes identity. For older acknowledged-protocol senders without
`source_id`, the server falls back to `source` as identity.

Each sync/reset/append carries the source file modification time in `mod_time`.
The server uses it for `updated_at`, preserving upstream file-based session ordering
and avoiding marking historical files as live during restart/replay.

After every successful ACK, `FileState` records the committed offset, generation,
and SHA-256 prefix digest. On a changed file, the watcher verifies that prefix and
reads the suffix. Prefix verification deliberately reads the old prefix too: it detects
same-size overwrites and truncate/regrow events, not just smaller files. This costs
O(committed file size) hashing on changes and can be optimized later with an equally
strong replacement-detection contract.

On any interrupted exchange, the watcher closes that connection, clears cached
file states, and reconnects with bounded exponential backoff (100ms to 5s). There is
one reconnect owner, not a goroutine per failed flush. Cancellation closes in-flight
socket I/O and stops retrying. There is no RAM queue to drain or silently discard;
unacknowledged/unread records remain in the source file for the next run. Shutdown
does not promise to upload the final suffix while the server is unavailable.

## Watcher/server protocol (v2)

Upgrade both binaries together. The old fire-and-forget `line` protocol is rejected.
Wire types live in `watcher/protocol.go` and `server/protocol.go` and must stay aligned.

1. `sync`: `{ "type":"sync", "source":"pi", "source_id":"<root-id>",
   "path":"pi/session.jsonl", "mod_time":"2026-01-04T10:15:30Z" }`
2. Server replies: `{ "type":"ack", "generation":"...", "offset":123,
   "digest":"<sha256-of-committed-original-bytes>" }`.
3. Watcher compares that prefix to the local file. If it matches, resume at `offset`.
   If it differs or the local file is shorter, send `reset` with the observed generation
   and offset. The server conditionally replaces the session, generates a fresh
   generation, and acknowledges an empty prefix.
4. `append`: `{ "type":"append", "source":"pi", "source_id":"<root-id>",
   "path":"pi/session.jsonl", "mod_time":"2026-01-04T10:15:30Z",
   "generation":"...", "offset":123, "lines":["{...}\n", "{...}\n"] }`.
5. Server atomically commits and replies with generation and next byte offset.
   Append ACKs omit the digest; the watcher updates its local digest from the records.
6. `ping` receives `pong`. Error replies contain `type:"error"` and an `error` message.

Offsets count original bytes including LF/CRLF, not Unicode characters or records.
Each append element must contain exactly one terminating LF. Generation tokens
change on reset and server restart. Stale generations, gaps, conflicting retries,
and reset requests based on an outdated offset are rejected. Exact repeated batches
are acknowledged without being appended again. If an ACK is lost after commit,
reconnection sync discovers that committed prefix and resumes after it.

ACK means committed to server memory, **not** durable server storage. Server restart
rehydrates even idle sessions via the watcher's heartbeat/reconciliation. Files
written while disconnected are read on recovery. Deleted source files cannot be
recovered after a server restart; retained server sessions are not automatically
deleted when their source file disappears.

## SSE and browser behavior

`SSEBroadcaster` queues coalesced wakeups, not transcript data. It never blocks on a
subscriber. Every handler reads missing records from the authoritative session store
using its own cursor, copying at most 128 lines at a time and releasing store locks
before network I/O. Slow response writers therefore cannot lock out ingestion.
Writes have five-second deadlines; idle streams send heartbeats every ten seconds.

Session events:

```text
id: <generation>:0
event: reset
data: {"path":"pi/session.jsonl"}

id: <generation>:1
event: line
data: {"path":"pi/session.jsonl","line":"{...}","line_num":1,"source":"pi"}
```

The handler subscribes before snapshotting, then uses the cursor to avoid replay/live
overlap. `Last-Event-ID: <generation>:<line-number>` resumes after that record. A
missing, invalid, out-of-range or old-generation cursor causes reset plus replay.
A reset clears the browser transcript before replacement history arrives.

The browser keeps the same EventSource during transient errors so native reconnect
preserves `Last-Event-ID`. It ignores duplicate line numbers and stale callbacks after
session selection changes. There are no competing manual reconnect timers.

`/api/stream` now emits `event: sessions` containing a complete metadata array rather
than every raw transcript line. Full snapshots reconcile line-count decreases, newly
created sessions and server restarts even when wakeups coalesce. Selected transcript
streams remain `event: line` plus `event: reset`. Metadata includes the `source`
label and, for Claude sessions, the first user-message `preview`. `server/preview.go`
extracts that optional display metadata at ingestion, excluding meta/tool-result
messages; raw transcript records remain unchanged. Reset clears the preview too.
This preserves upstream previews for unselected sessions without restoring the
old global fire-and-forget line stream.

## Validation and code walkthrough

```bash
make test             # all three Go modules; unit + end-to-end integration tests
make test-ui          # Node.js 18+, built-in test runner; no npm packages
cd server && go vet ./...
cd ../watcher && go vet ./...
cd ../tests && go vet ./...
```

Where a supported C compiler/CGO is available, also run:

```bash
cd server && go test -race ./...
cd ../watcher && go test -race ./...
```

- `server/protocol_test.go`: exact retried batches, digests, source conflicts,
  gap/partial-record rejection, stale generation and reset preconditions.
- `server/stream_test.go`, `slow_test.go`, `sse_test.go`: slow subscriber cleanup,
  blocked response writes, burst catch-up, cursor resume/reset, metadata reconciliation.
- `watcher/main_test.go`: incomplete/large records, same-size/larger replacements,
  committed-but-lost ACK fault injection, offline reconnect and cancellation.
- `tests/recovery_test.go`, `concurrent_recovery_test.go`: actual child-process
  watcher/server restarts, idle replay, offline appends, truncation and startup appends.
- `tests/integration_test.go`: existing large-record, nested-directory and latency
  regressions. Watcher readiness waits for its acknowledged initial scan log, not a
  fixed sleep. Process output is captured for startup failures.
- `tests/multisource_test.go`: identical filenames in Pi/Claude roots, preserved
  source labels/previews/file timestamps, live appends and both process restarts.
- `watcher/config_test.go`, `server/metadata_test.go`: CLI path parsing/root boundaries,
  source-label versus root identity, preview reset and historical timestamp handling.
- `tests/stream.test.cjs`: browser stream-state logic, Pi/Claude renderer selection,
  previews and ordering using a mocked DOM/EventSource. This is not a full browser
  layout or end-to-end rendering test.

## Remaining work (not included)

- Safety defaults/authentication and untrusted-metadata HTML rendering remain as-is.
- Server memory retention is still unbounded; durable storage, pagination and transcript
  virtualization are separate work.
- Measure real browser rendering latency and add browser automation/CI across platforms.
- Optimize prefix validation without weakening overwrite/replacement detection.
