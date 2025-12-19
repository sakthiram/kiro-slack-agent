# Architecture

This document describes the technical architecture of the Amelia Slack Agent, including system design, component interactions, and implementation details.

## Table of Contents

- [System Overview](#system-overview)
- [Core Components](#core-components)
- [Message Flow](#message-flow)
- [Task Processing](#task-processing)
- [Comment Synchronization](#comment-synchronization)
- [Error Handling](#error-handling)
- [Concurrency Model](#concurrency-model)
- [Configuration](#configuration)

## System Overview

The Amelia Slack Agent is a Go-based service that bridges Slack conversations to the Kiro CLI agent using an **asynchronous, event-driven architecture**. It uses Slack's Socket Mode for real-time event delivery and **beads (bd)** for issue tracking and context management.

```
+------------------+     +------------------+     +------------------+
|   SLACK EVENTS   |---->| FEATURE PROCESSOR|---->|   BEADS (bd)     |
|  (Socket Mode)   |     |  (Create Issues) |     | (Feature/Task)   |
+------------------+     +------------------+     +------------------+
        ^                                                 |
        |                                                 v
        |                                        +------------------+
        |                                        |     POLLER       |
        |                                        |  (bd ready)      |
        +----------------------------------------+------------------+
              (Comment Sync)                              |
                                                          v
                                                 +------------------+
                                                 |  WORKER POOL     |
                                                 |  (N goroutines)  |
                                                 +------------------+
                                                          |
                                                          v
                                                 +------------------+
                                                 |  KIRO RUNNER     |
                                                 | (Non-Interactive)|
                                                 +------------------+
```

### Design Principles

1. **Async Processing**: Slack handler returns immediately; AI processing happens in background
2. **Issue-Driven**: Every conversation is tracked as a beads Feature/Task
3. **Scalable Workers**: Configurable pool of workers process tasks concurrently
4. **Comment Sync**: Agent responses flow back to Slack via beads comments
5. **Stateless Handler**: No sessions needed; context reconstructed from beads

## Core Components

### 1. Slack Handler

**Location**: `internal/slack/handler.go`

The Slack Handler manages Socket Mode events and routes messages to the Feature Processor.

**Responsibilities**:
- Listen for Socket Mode events (`app_mention`, `message.im`, `message.channels`)
- Parse and clean @mentions from message text
- Acknowledge events immediately
- Route to Feature Processor asynchronously
- Post acknowledgment messages to users

**Event Routing**:
```
Socket Mode Event
    │
    ├─▶ EventTypeConnected ────────▶ Log connection
    │
    ├─▶ EventTypeEventsAPI
    │       │
    │       ├─▶ app_mention ──────────┐
    │       ├─▶ message.im ───────────┼──▶ handleWithFeatureProcessor()
    │       └─▶ message.channels ─────┘
    │
    └─▶ EventTypeConnectionError ──▶ Log error
```

**Acknowledgment Messages**:
- Main posts: "📝 Received! Working on it..."
- Thread replies: "👍 Got it! Processing..."

### 2. Feature Processor

**Location**: `internal/processor/feature_processor.go`

The Feature Processor routes Slack messages to create beads issues (Features or Tasks).

**Responsibilities**:
- Determine if message is main post or thread reply
- Create Feature issues for main posts (new conversations)
- Create Task issues for thread replies (under parent Feature)
- Add user message as comment on issue
- Register issues with Comment Syncer for response delivery

**Routing Logic**:
```go
if msg.ThreadTS == "" || msg.ThreadTS == msg.MessageTS {
    // Main post → Create Feature
    ProcessMainPost(ctx, msg)
} else {
    // Thread reply → Create Task under parent Feature
    ProcessThreadReply(ctx, msg)
}
```

**Issue Structure**:
```
Feature (slack-xxx)           ← Main Slack post
├── Title: First line of message
├── Description: Full message text
├── Labels: thread:<ts>, channel:<id>, user:<id>
├── Comments:
│   ├── [user] Original message
│   └── [agent] Kiro response (synced to Slack)
│
└── Task (slack-xxx.1)        ← Thread reply
    ├── Title: Reply message
    ├── Parent: Feature ID
    └── Comments: ...
```

### 3. Beads Manager

**Location**: `internal/beads/manager.go`

The Beads Manager wraps the `bd` CLI for issue CRUD operations.

**Key Methods**:
```go
// User directory management
EnsureUserDir(ctx, userID) (string, error)  // Creates user session dir, runs bd init
GetUserDir(userID) string                    // Returns path to user's beads dir
ListUserDirs() []string                      // Lists all user directories

// Issue operations
FindThreadIssue(ctx, userID, thread) (*Issue, error)    // Find by thread labels
CreateFeature(ctx, userID, thread, title, desc) error   // bd create -t feature
CreateTask(ctx, userID, parentID, thread, title) error  // bd create -t task --parent
GetIssue(ctx, userID, issueID) (*Issue, error)          // bd show --json

// Task status
GetReadyTasks(ctx, userID) ([]ReadyTask, error)         // bd ready --json
UpdateTaskStatus(ctx, userID, issueID, status) error    // bd update --status

// Comments
UpdateThreadIssue(ctx, userID, issueID, role, msg) error  // Add comment
AddAgentComment(ctx, userID, issueID, content) error      // Add [agent] prefixed comment
```

**BD Commands Used**:
| Method | BD Command |
|--------|------------|
| `EnsureUserDir` | `bd init` |
| `CreateFeature` | `bd create -t feature --label ...` |
| `CreateTask` | `bd create -t task --parent ...` |
| `GetReadyTasks` | `bd ready --json` |
| `UpdateTaskStatus` | `bd update <id> --status <status>` |
| `AddAgentComment` | `bd comment <id> "[agent] ..."` |

### 4. Task Queue

**Location**: `internal/queue/`

The Task Queue manages work items waiting to be processed.

**Files**:
- `types.go`: TaskWork and TaskResult structs
- `queue.go`: Channel-based queue implementation
- `poller.go`: Polls `bd ready` for new tasks

**TaskWork Structure**:
```go
type TaskWork struct {
    IssueID    string            // Beads issue ID
    UserID     string            // User who owns the session
    ThreadInfo *beads.ThreadInfo // Slack thread context
    Priority   int               // Task priority (from issue)
    Retries    int               // Current retry count
    MaxRetries int               // Max retries allowed
    CreatedAt  time.Time
}
```

**Poller Loop**:
```
Every poll_interval (default 10s):
    │
    ├─▶ List all user directories
    │
    └─▶ For each user:
            │
            ├─▶ Run `bd ready --json`
            │
            └─▶ Enqueue each ready task
```

### 5. Worker Pool

**Location**: `internal/worker/`

The Worker Pool processes tasks concurrently using Kiro CLI.

**Files**:
- `pool.go`: WorkerPool management
- `worker.go`: Individual worker logic
- `runner.go`: KiroRunner for CLI execution

**Worker Flow**:
```
Worker.processTask(task)
    │
    ├─▶ Update status to "in_progress"
    │       bd update <id> --status in_progress
    │
    ├─▶ Get issue details
    │       bd show <id> --json
    │
    ├─▶ Build prompt from issue + comments
    │
    ├─▶ Run Kiro CLI (non-interactive)
    │       kiro-cli chat --agent --trust-all-tools \
    │           --no-interactive --wrap never "<prompt>"
    │
    ├─▶ Add agent comment with response
    │       bd comment <id> "[agent] <response>"
    │
    ├─▶ Update status to "completed" (or "blocked" on error)
    │
    └─▶ Trigger comment sync to Slack
```

**KiroRunner**:
```go
func (r *KiroRunner) Run(ctx context.Context, workDir, prompt string) (string, error) {
    cmd := exec.CommandContext(ctx, r.binaryPath,
        "chat", "--agent", "--trust-all-tools",
        "--no-interactive", "--wrap", "never",
        prompt)
    cmd.Dir = workDir
    cmd.Env = append(os.Environ(), "TERM=dumb")

    output, err := cmd.CombinedOutput()
    // Parse and return response
}
```

### 6. Comment Syncer

**Location**: `internal/sync/`

The Comment Syncer posts agent responses from beads back to Slack threads.

**Files**:
- `tracker.go`: Tracks sync state per issue
- `syncer.go`: Sync logic and loop

**Sync State**:
```go
type SyncState struct {
    IssueID          string
    UserID           string
    ChannelID        string
    SlackThreadTS    string
    SyncedCommentIDs map[string]bool  // Already synced comments
}
```

**Sync Loop**:
```
Every sync_interval (default 5s):
    │
    └─▶ For each registered issue:
            │
            ├─▶ Get issue comments from beads
            │
            └─▶ For each [agent] comment not yet synced:
                    │
                    ├─▶ Post to Slack thread
                    │
                    └─▶ Mark as synced
```

**Comment Format**:
- Agent comments are prefixed with `[agent] ` in beads
- Prefix is stripped when posting to Slack
- Only `[agent]` comments are synced (not user comments)

## Message Flow

### Complete Flow Diagram

```
┌─────────┐
│  User   │
└────┬────┘
     │ "help me code"
     ▼
┌─────────────────┐
│  Slack Socket   │
│      Mode       │
└────┬────────────┘
     │ EventsAPI (app_mention or message)
     ▼
┌─────────────────┐
│     Handler     │◀──── Parse & Clean Message
│  (handler.go)   │
└────┬────────────┘
     │ Async dispatch
     ▼
┌─────────────────────────────────┐
│     Feature Processor           │
│  (feature_processor.go)         │
└────┬────────────────────────────┘
     │
     ├──▶ Ensure user beads directory
     │        bd init (if needed)
     │
     ├──▶ Create Feature or Task
     │        bd create -t feature/task --label ...
     │
     ├──▶ Add user message as comment
     │        bd comment <id> "[user] message"
     │
     ├──▶ Register with Syncer
     │
     └──▶ Return (handler posts acknowledgment)


┌─────────────────┐
│     Poller      │  (Every 10s)
└────┬────────────┘
     │
     ├──▶ bd ready --json (for each user)
     │
     └──▶ Enqueue tasks to TaskQueue


┌─────────────────┐
│  Worker Pool    │
└────┬────────────┘
     │
     ├──▶ Dequeue task
     │
     ├──▶ bd update <id> --status in_progress
     │
     ├──▶ Build prompt from issue
     │
     ├──▶ kiro-cli chat --no-interactive "<prompt>"
     │
     ├──▶ bd comment <id> "[agent] <response>"
     │
     └──▶ bd update <id> --status completed


┌─────────────────┐
│  Comment Syncer │  (Every 5s)
└────┬────────────┘
     │
     ├──▶ Get issue comments
     │
     └──▶ Post [agent] comments to Slack thread
```

### Main Post vs Thread Reply

**Main Post** (new conversation):
```
User posts in channel: "@AmeliaBot help me write a script"
    │
    └──▶ ProcessMainPost()
            │
            ├──▶ CreateFeature("help me write a script", ...)
            │       Type: feature
            │       Labels: thread:1234.5678, channel:C123, user:U456
            │
            └──▶ Response: "📝 Received! Working on it..."
```

**Thread Reply** (continue conversation):
```
User replies in thread: "Now add error handling"
    │
    └──▶ ProcessThreadReply()
            │
            ├──▶ FindThreadIssue() → parent Feature
            │
            ├──▶ CreateTask("Now add error handling", parent=<feature-id>)
            │       Type: task
            │       Parent: slack-xxx (the Feature)
            │
            └──▶ Response: "👍 Got it! Processing..."
```

## Task Processing

### Task States

```
┌─────────┐     Poller finds      ┌─────────────┐
│  open   │ ───────────────────▶  │   ready     │
└─────────┘     (bd ready)        └──────┬──────┘
                                         │
                                         │ Worker dequeues
                                         ▼
                                  ┌─────────────┐
                                  │ in_progress │
                                  └──────┬──────┘
                                         │
                          ┌──────────────┼──────────────┐
                          │              │              │
                          ▼              ▼              ▼
                   ┌──────────┐   ┌──────────┐   ┌──────────┐
                   │completed │   │ blocked  │   │   open   │
                   └──────────┘   └──────────┘   └──────────┘
                     Success      Error/Block    Retry (back to queue)
```

### Prompt Building

The worker builds a prompt from the issue and its comments:

```go
func buildPrompt(issue *beads.Issue) string {
    var sb strings.Builder

    sb.WriteString("Context:\n")
    sb.WriteString(issue.Description)
    sb.WriteString("\n\nConversation:\n")

    for _, comment := range issue.Comments {
        role := "User"
        if strings.HasPrefix(comment.Content, "[agent]") {
            role = "Assistant"
        }
        sb.WriteString(fmt.Sprintf("%s: %s\n", role, comment.Content))
    }

    sb.WriteString("\nPlease respond to the latest message.")
    return sb.String()
}
```

### Retry Logic

```go
type TaskWork struct {
    Retries    int  // Current retry count
    MaxRetries int  // From config (default 2)
}

// On worker error:
if task.Retries < task.MaxRetries {
    task.Retries++
    time.Sleep(cfg.Worker.RetryBackoff)  // default 30s
    queue.Add(task)  // Re-enqueue
} else {
    beadsMgr.UpdateTaskStatus(ctx, userID, issueID, "blocked")
}
```

## Comment Synchronization

### Registration

When Feature Processor creates an issue:

```go
syncer.RegisterIssue(issueID, userID, &beads.ThreadInfo{
    ChannelID: msg.ChannelID,
    ThreadTS:  threadTS,
    UserID:    msg.UserID,
})
```

### Sync Algorithm

```go
func (s *CommentSyncer) SyncIssue(ctx context.Context, issueID string) error {
    state := s.tracker.Get(issueID)
    if state == nil {
        return nil  // Not registered
    }

    issue, _ := s.beadsMgr.GetIssue(ctx, state.UserID, issueID)

    for _, comment := range issue.Comments {
        // Skip already synced
        if state.SyncedCommentIDs[comment.ID] {
            continue
        }

        // Only sync [agent] comments
        if !strings.HasPrefix(comment.Content, "[agent]") {
            continue
        }

        // Post to Slack
        content := strings.TrimPrefix(comment.Content, "[agent] ")
        s.slackClient.PostMessage(ctx, state.ChannelID, content,
            slack.WithThreadTS(state.SlackThreadTS))

        state.SyncedCommentIDs[comment.ID] = true
    }
}
```

## Error Handling

### Levels of Error Handling

1. **Feature Processor Errors**
   - Log error
   - Post error message to Slack thread
   - User can retry by posting again

2. **Worker Errors**
   - Retry with backoff (up to MaxRetries)
   - Mark issue as "blocked" after max retries
   - Log detailed error for debugging

3. **Sync Errors**
   - Skip failed comment, retry next interval
   - Log warning
   - Eventually consistent

### Common Error Scenarios

| Error | Handling | Result |
|-------|----------|--------|
| `bd init` fails | Log error, return | User sees error message |
| `bd create` fails | Log error, return | User sees error message |
| `kiro-cli` timeout | Retry up to MaxRetries | Mark blocked if all fail |
| Slack API error | Log, continue | Comment not synced (retry later) |
| User directory missing | `EnsureUserDir` creates it | Transparent to user |

## Concurrency Model

### Goroutine Architecture

```
Main Goroutine
    │
    ├──▶ Socket Mode Event Loop (1 goroutine)
    │        │
    │        └──▶ Per-event handler (async, N goroutines)
    │
    ├──▶ Worker Pool (N goroutines, configurable)
    │        │
    │        └──▶ Each worker processes tasks sequentially
    │
    ├──▶ Poller (1 goroutine)
    │        │
    │        └──▶ Polls every poll_interval
    │
    ├──▶ Comment Syncer (1 goroutine)
    │        │
    │        └──▶ Syncs every sync_interval
    │
    └──▶ Signal Handler (1 goroutine)
             │
             └──▶ Graceful shutdown on SIGTERM/SIGINT
```

### Thread Safety

| Component | Synchronization |
|-----------|-----------------|
| TaskQueue | Go channels (inherently safe) |
| SyncTracker | `sync.RWMutex` |
| WorkerPool | Context cancellation |
| BeadsManager | Stateless (each call is independent) |

### Graceful Shutdown

```go
// In main.go
ctx, cancel := context.WithCancel(context.Background())

// Start services
go workerPool.Start(ctx)
go poller.Start(ctx)
go syncer.StartSyncLoop(ctx, interval)

// On SIGTERM/SIGINT:
cancel()              // Signal all goroutines
workerPool.Stop()     // Wait for workers to finish
taskQueue.Close()     // Release blocked readers
time.Sleep(500ms)     // Allow cleanup
```

## Configuration

### Worker Configuration

```yaml
worker:
  pool_size: 3           # Number of concurrent workers
  poll_interval: 10s     # How often to poll bd ready
  task_timeout: 5m       # Max time for kiro-cli execution
  max_retries: 2         # Retry count on failure
  retry_backoff: 30s     # Wait between retries
```

### Sync Configuration

```yaml
sync:
  sync_interval: 5s      # How often to sync comments
  enabled: true          # Enable/disable comment sync
```

### Beads Configuration

```yaml
beads:
  # Persistent path for user sessions (each user gets their own .beads directory)
  # Structure: {sessions_base_path}/{user_id}/.beads/
  sessions_base_path: "/Users/youruser/.kiro-agent/sessions"  # Use persistent path!
  issue_prefix: "slack"                                        # Prefix for issue IDs
  context_max_messages: 20                                     # Max messages in context
```

**Important**: Use a persistent path (not `/tmp`) to preserve data across restarts.

**Directory Structure:**
```
{sessions_base_path}/
├── {user_id_1}/                    # Slack user ID (e.g., W0175971WA3)
│   └── .beads/
│       ├── beads.db                # SQLite database
│       ├── issues.jsonl            # JSONL backup (human-readable)
│       ├── config.yaml             # Beads config
│       └── metadata.json
├── {user_id_2}/
│   └── .beads/
└── ...
```

Each user gets isolated storage - their conversations, issues, and context are separate.

### Kiro Configuration

```yaml
kiro:
  binary_path: "kiro-cli"                    # Path to kiro-cli
  response_timeout: 120s                     # Timeout for CLI execution
```

## Performance Characteristics

### Latency

| Operation | Expected Latency |
|-----------|------------------|
| Slack → Handler | <100ms (Socket Mode) |
| Feature/Task Creation | <500ms (bd create) |
| Poll Interval | 10s (configurable) |
| AI Response | 5-120s (depends on query) |
| Comment Sync | <5s after response |

### Resource Usage

| Resource | Usage |
|----------|-------|
| Memory | ~10MB base + ~5MB per worker |
| CPU | Low (mostly I/O bound) |
| Disk | ~1KB per issue in beads |
| Processes | 1 kiro-cli per active worker |

### Scaling

| Component | Scaling Strategy |
|-----------|------------------|
| Workers | Increase `pool_size` |
| Users | Add more user directories |
| Issues | Beads handles 1000s of issues |
| Slack | Single Socket Mode connection |

For higher scale:
- Deploy multiple instances behind load balancer
- Use shared storage for beads directories
- Consider message queue (SQS, Redis) instead of channels

## Troubleshooting

### Enable Debug Logging

```yaml
logging:
  level: "debug"
  format: "console"
```

### Check Beads State

```bash
# Navigate to user's beads directory
cd {sessions_base_path}/<user-id>
# e.g., cd /Users/youruser/.kiro-agent/sessions/W0175971WA3

# List all issues
bd list

# List by status
bd list --status open
bd list --status closed

# Show issue details
bd show <issue-id> --json

# Check ready tasks (what poller picks up)
bd ready --json
```

### Test Kiro CLI Standalone

```bash
cd {sessions_base_path}/<user-id>
kiro-cli chat --agent --trust-all-tools --no-interactive "hello"
```

### Monitor Worker Pool

Check logs for:
- `worker started` / `worker stopped`
- `processing task` / `task completed`
- `task failed` / `retrying task`

### Verify Slack Connection

Check logs for:
- `connected to Slack`
- `Socket Mode connected`
- `handling app_mention` / `handling message`

## References

- [Slack Socket Mode Documentation](https://api.slack.com/apis/connections/socket)
- [slack-go/slack Library](https://github.com/slack-go/slack)
- [Beads Issue Tracker](https://github.com/beads-cli/beads)
- [Kiro CLI Documentation](https://github.com/kiro-cli)
