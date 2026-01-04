# Epic Plan: mvp-review

This plan is intentionally scoped to **issue creation and handoff**. No code changes are made as part of this epic.

## Phase 0 — Baseline capture (done)
- [x] Run test suite to capture failures: `make test-quick`
- [x] Identify likely root causes by code review
- [x] Consolidate findings into `design.md`

## Phase 1 — Create bd epic + child issues (next)
Create one bd epic `mvp-review` with child issues that are self-sufficient.

### Issue set (proposed)
1. **Watcher: prevent batch loss when disconnected**
   - Fix send semantics so a disconnected state can’t be treated as success.
   - Validation: existing live update tests pass.

2. **Watcher: handle atomic rename file creation**
   - Handle fsnotify `Rename`/`Chmod` patterns and ensure new `.jsonl` files are read.
   - Validation: `TestNewFileCreation` passes; add/adjust regression coverage if needed.

3. **Watcher: robust nested directory discovery**
   - Ensure newly created directory trees become watched and scanned.
   - Validation: `TestNestedDirectoryCreation` and `TestInitialScan_NestedStructure` pass.

4. **Watcher: increase scanner buffer for long JSONL lines**
   - Prevent read failures on long lines.
   - Validation: add new test that writes a >64KB JSON line and ensure it is transmitted.

5. **Watcher: define backpressure policy + observability**
   - Decide and document: drop-oldest vs block vs spill-to-disk.
   - At minimum, log/measure when drops occur.
   - Validation: new test or manual validation that drop behavior is visible.

6. **Server: memory growth & logging follow-ups** (optional, can be split)
   - Add configurable retention / limits.
   - Gate MD5 logging behind debug.
   - Validation: memory usage bounded for large sessions; logs quieter by default.

## Phase 2 — Execution (future work, not part of this epic)
- Implement issues using TDD.
- Keep integration tests as primary validation.

## Validation for this epic
- `devdocs/mvp-review/design.md` exists and is self-contained.
- `devdocs/mvp-review/plan.md` exists with phases and validation.
- `bd` epic and child issues exist with:
  - What to build
  - References
  - Validation commands
