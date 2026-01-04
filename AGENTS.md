## Development Documentation (devdocs/)

The `devdocs/` directory tracks multi-phase tasks and preserves key learnings.

**For in-progress tasks:** Create `devdocs/<task>/plan.md` and `progress.md` to track work across sessions.

**For completed tasks:** Extract key learnings into `devdocs/archive/<task>.md` and delete the plan/progress files.

**Reference docs:** `DEBUGGING.md`, `memory-model.md`, `lit-tests.md` contain active technical documentation.

See `devdocs/README.md` for the full methodology.

**Important:** Task creation and archival are user-initiated. The agent should:
- Update `progress.md` as work progresses
- Suggest when a task might be ready for archival
- Only create/archive tasks when explicitly asked by the user
