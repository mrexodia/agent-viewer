# Development Documentation

This directory contains development documentation for the project.

## Directory Structure

```
devdocs/
├── README.md           # This file
├── <topic>.md          # Additional documentation on <topic>
├── archive/            # Completed task summaries (key learnings only)
│   ├── <task-name>.md
└── <task-name>/        # In-progress tasks
    ├── design.md
    └── plan.md
```

## Task Tracking Methodology

### In-Progress Tasks

For larger multi-phase tasks, create a subdirectory with:

```
devdocs/<task-name>/
├── design.md     # Optional: user-provided design document
└── plan.md       # Goals, phases, required APIs, testing strategy
```

**During development:**
1. Use `bd` for task tracking

### Completed Tasks → Archive

When a task is complete and the user asks you to archive:

1. **Extract key learnings** into `archive/<task-name>.md`:
   - Goal and scope (1 paragraph)
   - Key architectural decisions
   - Technical insights and gotchas
   - API summary (what was built)
   - If the design document exists, integrate it into the existing repository documentation
   - References to related docs

2. **Delete the task directory** (`plan.md`)
   - Detailed progress is preserved in git history
   - Only essential knowledge lives in the archive

3. **Archive the design** (`design.md`)
   - Ask the user whether to move `design.md` to `../<taskname>.md` as permanent documentation

### Archive Contents

The `archive/` directory contains summaries of completed work.

<list_here>

These are **reference documents**, not progress trackers. They capture:
- What was built and why
- Design decisions that affect future work
- Technical gotchas to avoid repeating mistakes

## Agent Instructions

**Important:** Task creation and archival are **user-initiated**. The agent should not create new task directories or archive completed tasks without explicit user request.

**When working on an existing task:**
1. Use `bd` to work on incomplete items
2. Suggest when a task might be ready for archival (but don't archive without asking)

**When user asks to create a new task:**
1. Create `devdocs/<task-name>/plan.md` with goals and phases
2. Create issues in `bd` for tracking (with an epic for the <task-name>)

**When user asks to archive a completed task:**
1. Make sure all the issues are closed
2. Create `archive/<task-name>.md` with key learnings
3. Delete the task directory (plan.md)

**Looking up past decisions:**
1. Check `archive/` for relevant completed tasks
2. Check reference docs
