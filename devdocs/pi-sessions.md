Here are the type definitions for JSONL session files:

File Location

/Users/admin/Projects/pi-mono/packages/coding-agent/dist/core/session-manager.d.ts

Key Types

### Session Header

```typescript
interface SessionHeader {
    type: "session";
    version?: number;  // v1 sessions don't have this
    id: string;
    timestamp: string;
    cwd: string;
    parentSession?: string;
}
```

### Base Entry Type

```typescript
interface SessionEntryBase {
    type: string;
    id: string;
    parentId: string | null;
    timestamp: string;
}
```

### Entry Types

```typescript
// Message entry
interface SessionMessageEntry extends SessionEntryBase {
    type: "message";
    message: AgentMessage;
}

// Thinking level change
interface ThinkingLevelChangeEntry extends SessionEntryBase {
    type: "thinking_level_change";
    thinkingLevel: string;
}

// Model change
interface ModelChangeEntry extends SessionEntryBase {
    type: "model_change";
    provider: string;
    modelId: string;
}

// Compaction summary
interface CompactionEntry<T = unknown> extends SessionEntryBase {
    type: "compaction";
    summary: string;
    firstKeptEntryId: string;
    tokensBefore: number;
    details?: T;
    fromHook?: boolean;
}

// Branch summary
interface BranchSummaryEntry<T = unknown> extends SessionEntryBase {
    type: "branch_summary";
    fromId: string;
    summary: string;
    details?: T;
    fromHook?: boolean;
}

// Custom entry (hook state, not sent to LLM)
interface CustomEntry<T = unknown> extends SessionEntryBase {
    type: "custom";
    customType: string;
    data?: T;
}

// Custom message entry (participates in LLM context)
interface CustomMessageEntry<T = unknown> extends SessionEntryBase {
    type: "custom_message";
    customType: string;
    content: string | (TextContent | ImageContent)[];
    details?: T;
    display: boolean;
}

// Label entry
interface LabelEntry extends SessionEntryBase {
    type: "label";
    targetId: string;
    label: string | undefined;
}
```

### Union Types

```typescript
type SessionEntry =
    | SessionMessageEntry
    | ThinkingLevelChangeEntry
    | ModelChangeEntry
    | CompactionEntry
    | BranchSummaryEntry
    | CustomEntry
    | CustomMessageEntry
    | LabelEntry;

type FileEntry = SessionHeader | SessionEntry;
```

### Additional Types

```typescript
interface SessionTreeNode {
    entry: SessionEntry;
    children: SessionTreeNode[];
    label?: string;
}

interface SessionContext {
    messages: AgentMessage[];
    thinkingLevel: string;
    model: { provider: string; modelId: string } | null;
}

interface SessionInfo {
    path: string;
    id: string;
    created: Date;
    modified: Date;
    messageCount: number;
    firstMessage: string;
    allMessagesText: string;
}
```

The current session version is CURRENT_SESSION_VERSION = 2. Each line in the JSONL file is a JSON object representing either a SessionHeader or one of the SessionEntry types.
