# Epic: mvp-review — MVP Codebase Review Findings

## Problem statement
The current Agent Session Viewer MVP has the right overall architecture (watcher → server → browser), but end-to-end reliability is insufficient: the integration test suite fails on nested directory detection, new file detection, and live update delivery/latency.

This epic consolidates the issues discovered during a codebase review **without implementing fixes**. It is intended to produce a self-contained set of actionable issues that a future session can pick up and implement using TDD.

## Goals
- Capture all review findings as concrete, self-sufficient issues.
- Provide references to exact files/areas.
- Define validation steps (commands + expected outcomes).
- Define a phased plan that can be completed across sessions.

## Non-goals
- No code changes in this epic.
- No refactors unless required by later issues.
- No feature additions beyond reliability/correctness and minimal hardening noted below.

## Current health check (baseline)
### Test status
`make test-quick` currently fails with:
- `TestInitialScan_NestedStructure`
- `TestLiveUpdate_SingleAppend`
- `TestLiveUpdate_RapidAppends`
- `TestNewFileCreation`
- `TestLatency_FileToServer`
- `TestNestedDirectoryCreation`

(See `tests/integration_test.go` for expected behaviors.)

## Findings

### A) Watcher correctness & reliability (highest priority)
**Files**: `watcher/main.go`

#### A1. Batch send semantics drop data when disconnected
- `sendLine()` returns `nil` if `w.conn == nil`.
- `batchSender()` interprets `nil` as success and will clear the batch.
- Result: data loss during disconnect/reconnect windows; can explain missing appended lines and latency test failures.

**Impact**: correctness (lost lines), test failures, unreliable behavior in real usage.

#### A2. File creation via atomic rename not handled (missed fsnotify events)
- `handleFSEvents()` handles `Write|Create` only.
- Many writers create temp file then `Rename` into place.
- If `.jsonl` arrives by rename, watcher may never read it.

**Impact**: new file creation tests and real-world usage can miss sessions.

#### A3. Nested directory creation not robust
- Startup walk adds watches for existing directories.
- New directories are added only if a `Create` event for the directory is observed.
- If intermediate dirs are created quickly or events coalesce, adding watches can be missed.

**Impact**: `TestNestedDirectoryCreation` and nested session detection failures.

#### A4. `bufio.Scanner` default token limit can truncate/stop reading large JSONL lines
- `readFile()` uses `bufio.NewScanner` with default buffer limit (~64K).
- Real agent logs can exceed this.

**Impact**: production correctness; not necessarily covered by current tests.

#### A5. Backpressure and queue sizing behavior unclear
- `lineQueue` is buffered to 10,000; `batchSender` also keeps `batch` up to 10,000.
- During sustained disconnects or bursts, oldest lines are dropped.
- No explicit metrics or logs tie dropped lines to user-visible state.

**Impact**: silent data loss under load.

### B) Server concerns (medium priority)
**Files**: `server/main.go`

#### B1. Unbounded in-memory growth
- `Session.Lines` and `Session.RawContent` append forever.
- No limits/eviction/persistence.

**Impact**: memory blow-ups on large sessions; acknowledged in README but still operationally risky.

#### B2. Debug logging overhead and noise (MD5 per line)
- `AddLine()` computes MD5 for every line and logs it.

**Impact**: performance and log noise; should be gated (debug flag) if retained.

#### B3. Path handling hardening
- `/api/sessions/{path}` extracts via `TrimPrefix` and uses raw URL path segment.
- Potential issues: URL encoding, `..` traversal semantics (even if only used as map keys), inconsistent path normalization across OS.

**Impact**: correctness edge cases and future security hardening.

### C) Tests & tooling observations (medium/low priority)
**Files**: `tests/integration_test.go`, `Makefile`

#### C1. Tests are strong, but currently failing due to watcher reliability
- Focus should be: make watcher robust enough to satisfy existing tests.

#### C2. Potential flakiness from fixed sleeps
- Some tests rely on fixed sleeps (e.g., 200ms, 500ms) in addition to polling.
- This is acceptable for MVP but may be flaky on CI under load.

**Impact**: may need tuning once correctness bugs are fixed.

## Proposed remediation approach (TDD)
1. Make existing integration tests pass (do not weaken assertions).
2. Add targeted regression tests if a bug isn’t already covered (e.g., rename semantics, long line handling).
3. Only after correctness: address performance/memory/logging.

## Validation (how to know improvements worked)
- `make test` should pass.
- Manual:
  - Run server (`make server`) and watcher (`make watcher WATCH_DIR=...`) and confirm:
    - existing files show up
    - appended lines appear in browser quickly
    - new nested directories/files are discovered reliably

## References
- `watcher/main.go` — filesystem event handling, batching, reconnect
- `server/main.go` — session store, SSE broadcasting, handlers
- `tests/integration_test.go` — expected behavior and latency targets
- `README.md` — stated MVP targets and limitations
