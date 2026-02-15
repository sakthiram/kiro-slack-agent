# Task Controller UX Design

## Overview

The Task Controller provides a human-in-the-loop interface for managing agent tasks via Slack reactions and thread replies. It separates **system status** (automated, shown in status messages) from **human control** (user-initiated, via reactions and feedback).

## Status Messages

Every task gets a single Slack message in the thread that evolves as the task progresses. This message is the anchor for all status updates and human interactions.

### Lifecycle

```
⛔️ Task blocked: `id`        Poller detects blocked task, posts status
  > description               with blocker info
  > ⛔️ waiting on: `blocker`

👀 Task: `id`                 Task becomes ready (deps resolved)
  > description
  > 👀 3  ⏳ 1  ✅ 4          Thread task counts

⏳ Task: `id`                 Worker picks up task, agent starts
  > description
  > 👀 2  ⏳ 2  ✅ 4

✅ Task: `id`                 Agent completed successfully
  > description
  > 👀 2  ⏳ 1  ✅ 5
```

### Status Emoji Reference

| Emoji | State | Set By |
|-------|-------|--------|
| ⛔️ | Blocked by dependencies | Poller |
| 👀 | Queued, waiting for worker | Poller |
| ⏳ | Agent running | Worker |
| ✅ | Completed | Worker |
| 🔁1️⃣ | Failed, retrying (with attempt count) | Worker |
| ❌ | Failed, retries exhausted | Worker |
| ⏸️ | Paused by user | TaskController |
| 🏁 | Closed by user | TaskController |
| 💬 | Feedback received, restarting | TaskController |
| 👍 | Force-run by user | TaskController |

## Human Control via Reactions

Users react on a task's status message to control it. Reactions are processed by the `TaskController` via Slack's `reaction_added` and `reaction_removed` events.

### ⏸️ Pause

1. User adds ⏸️ reaction to status message
2. `TaskController.humanBlock`:
   - Sets in-memory `BlockTask` flag (instant, prevents retry race)
   - Adds `human:blocked` label to beads issue
   - Kills running agent process (via `CancelTask`)
   - Reopens task to `open` status
   - Updates status message to `⏸️ Task: ...`
3. Poller skips tasks with `human:blocked` label
4. Worker skips retry if `IsBlocked` returns true

### Remove ⏸️ (Resume)

1. User removes ⏸️ reaction
2. `TaskController.humanUnblock`:
   - Removes `human:blocked` label
   - Clears in-memory block and retry counter
   - Reopens task if not already open
   - Updates status message to `👀 Task: ...`
3. Poller picks up task on next cycle (normal beads flow)

### 👍 Force Run

1. User adds 👍 reaction
2. `TaskController.humanForceRun`:
   - Removes `human:blocked` label (if any)
   - Clears block and retry counter
   - Reopens task if needed
   - **Directly queues task** via `ForceQueue` (bypasses `bd ready`)
   - Updates status message to `👍 Task: ...`
3. Task runs immediately, even if beads dependencies are unresolved

### 🏁 Close

1. User adds 🏁 reaction
2. `TaskController.humanClose`:
   - Sets in-memory block (prevents retry)
   - Kills running agent
   - Runs `bd close` with reason "Closed by user"
   - Updates status message to `🏁 Task: ...`

## Feedback Mode

Users provide feedback to tasks by replying in the thread with the full task ID as a prefix.

### Format

```
`slackW0175971WA3-6ai` use the amelia-build skill to download from S3
```

### Rules

- Task ID must be at the **start** of the message
- Must be wrapped in backticks
- Must be a full ID (contains `-`)
- Task ID mentioned elsewhere in the message → normal new task

### Flow

1. Handler's `extractFeedbackTarget` parses the task ID from message start
2. `TaskController.HandleFeedback`:
   - Finds task by ID via `bd show` (works for any status including closed)
   - Adds `[user] <feedback>` comment to the task
   - Kills running agent (if any)
   - Reopens task (if closed or in_progress)
   - Resets retry counter (fresh attempts)
   - Updates status message to `💬 Task: ...`
3. Poller picks up the reopened task
4. Agent sees the new `[user]` comment in its conversation context

## Race Condition Handling

### ⏸️ During Agent Execution

The main race: user pauses while agent is mid-execution.

```
Timeline:
  humanBlock() → BlockTask() [in-memory, instant]
  humanBlock() → AddLabel("human:blocked") [bd update, ~200ms]
  humanBlock() → CancelTask() → kills agent process
  Worker sees failure → checks IsBlocked() → true → skips retry ✅
```

Key: `BlockTask()` sets the in-memory flag **before** `CancelTask()`. The worker's retry check uses the in-memory flag, not the beads label (which has write latency).

### Server Restart

On startup:
1. `ResetInProgressTasks` — all `in_progress` tasks reset to `open`
2. In-memory `blocked` map is empty, but poller checks `human:blocked` label
3. `HasLabel` correctly matches exact labels (fixed: was broken for valueless labels)

### Poller vs Worker Status Message Race

The poller updates status messages for ready tasks (⛔️→👀). The worker updates for active tasks (⏳→✅). To prevent the poller from overwriting the worker's ⏳ with 👀:
- Poller only updates to 👀 if the task is NOT already in the pending queue (`HasPending` check)

### Retry Counter Across Restarts

The queue's `completed` map tracks attempt counts per task. This prevents the poller from infinitely re-queuing tasks that the agent leaves `open` after exit. User actions (`HandleFeedback`, `humanForceRun`, `humanUnblock`) call `ResetTask` to clear the counter.

## Implementation

### Key Files

| File | Responsibility |
|------|---------------|
| `internal/processor/task_controller.go` | Reaction handling, feedback routing, human control |
| `internal/status/format.go` | Status message formatting |
| `internal/queue/queue.go` | `blocked` map, `completed` map, `BlockTask`/`UnblockTask` |
| `internal/queue/poller.go` | Blocked task status messages, `human:blocked` skip |
| `internal/worker/worker.go` | Status message post/update, retry gating |
| `internal/slack/handler.go` | `reaction_added`/`reaction_removed` event routing |
| `internal/beads/types.go` | `HasLabel`, `LabelValue`, `Dependency` type |
| `internal/beads/manager.go` | `GetBlockedTasks`, `RemoveLabel`, `FindIssueByStartedTS` |

### Beads Labels Used

| Label | Purpose |
|-------|---------|
| `started:<slack_ts>` | Links task to its Slack status message |
| `human:blocked` | Task paused by user, poller skips |
| `thread:<ts>` | Groups tasks by Slack thread |
| `channel:<id>` | Slack channel for posting |
| `msg:<ts>` | User's original message (for 👀 removal) |
| `synced:<comment_id>` | Tracks synced agent comments |
