# Architecture

This document describes the technical architecture of the Amelia Slack Agent, including system design, component interactions, and implementation details.

## Table of Contents

- [System Overview](#system-overview)
- [Core Components](#core-components)
- [Message Flow](#message-flow)
- [Session Lifecycle](#session-lifecycle)
- [Streaming Architecture](#streaming-architecture)
- [Error Handling](#error-handling)
- [Data Persistence](#data-persistence)
- [Concurrency Model](#concurrency-model)

## System Overview

The Amelia Slack Agent is a Go-based service that bridges Slack conversations to the Kiro CLI agent. It uses Slack's Socket Mode for real-time event delivery and manages persistent sessions backed by SQLite.

```
┌─────────────────────────────────────────────────────────────────┐
│                        Amelia Slack Agent                        │
│                                                                  │
│  ┌──────────────┐    ┌─────────────┐    ┌──────────────┐      │
│  │   Slack      │    │   Session   │    │    Kiro      │      │
│  │   Handler    │───▶│   Manager   │───▶│   Bridge     │      │
│  └──────────────┘    └─────────────┘    └──────────────┘      │
│         │                    │                   │              │
│         │                    │                   │              │
│  ┌──────▼──────┐    ┌────────▼──────┐   ┌───────▼────────┐   │
│  │  Streamer   │    │  SQLite Store │   │  PTY Process   │   │
│  └─────────────┘    └───────────────┘   └────────────────┘   │
│         │                                         │             │
└─────────┼─────────────────────────────────────────┼────────────┘
          │                                         │
          ▼                                         ▼
    Slack API                                   kiro-cli
```

### Design Principles

1. **Session Isolation**: Each Slack thread = independent Kiro CLI process
2. **Stateful Persistence**: Sessions survive restarts via SQLite
3. **Real-time Streaming**: Progressive message updates as responses arrive
4. **Graceful Degradation**: Retry logic and timeout handling
5. **Resource Management**: Automatic cleanup of idle sessions

## Core Components

### 1. Slack Handler

**Location**: `internal/slack/handler.go`

The Slack Handler manages Socket Mode events and routes messages to the appropriate processor.

**Responsibilities**:
- Listen for Socket Mode events
- Parse `app_mention` and `message.im` events
- Clean @mentions from message text
- Acknowledge events immediately
- Dispatch to message processor asynchronously

**Key Methods**:
```go
RegisterHandlers(socketClient)  // Sets up event loop
HandleEvent(evt, socketClient)  // Routes Socket Mode events
handleAppMention(ev)            // Processes @mentions
handleMessage(ev)               // Processes DMs
```

**Event Flow**:
```
Socket Mode Event
    │
    ├─▶ EventTypeConnected ────────▶ Log connection
    │
    ├─▶ EventTypeEventsAPI
    │       │
    │       ├─▶ app_mention ────────▶ handleAppMention()
    │       └─▶ message.im ─────────▶ handleMessage()
    │
    └─▶ EventTypeConnectionError ──▶ Log error
```

### 2. Session Manager

**Location**: `internal/session/manager.go`

The Session Manager maintains the lifecycle of Kiro CLI sessions, including creation, tracking, and cleanup.

**Responsibilities**:
- Map Slack threads to Kiro sessions (thread_ts → session)
- Enforce session limits (per-user and total)
- Create session working directories
- Persist session state to SQLite
- Automatic cleanup of idle sessions
- Graceful shutdown

**Key Methods**:
```go
GetOrCreate(ctx, channelID, threadTS, userID)  // Get or create session
UpdateActivity(ctx, id)                        // Mark session active
UpdateStatus(ctx, id, status)                  // Change session status
Close(ctx, id)                                 // Terminate and cleanup
Cleanup(ctx)                                   // Remove idle sessions
```

**Session States**:
- `active`: Available for new messages
- `processing`: Currently handling a message
- `error`: Failed state (will be cleaned up)

**Cleanup Process**:
```
Periodic Timer (5 min)
    │
    ▼
List Sessions (idle > 30 min)
    │
    ├─▶ Skip if status == processing
    │
    ├─▶ Close session
    │   ├─▶ Remove working directory
    │   └─▶ Delete from SQLite
    │
    └─▶ Log cleanup count
```

### 3. Kiro Bridge

**Location**: `internal/kiro/process.go`, `internal/kiro/bridge.go`, `internal/kiro/retry_bridge.go`

The Kiro Bridge manages communication with the Kiro CLI via a pseudo-terminal (PTY).

**Architecture**:
```
┌─────────────────┐
│  RetryBridge    │  Wrapper with retry logic
└────────┬────────┘
         │
         ▼
┌─────────────────┐
│    Process      │  PTY-based CLI interaction
└────────┬────────┘
         │
         ▼
┌─────────────────┐
│   PTY (pty.go)  │  Terminal interface
└────────┬────────┘
         │
         ▼
┌─────────────────┐
│    kiro-cli     │  Actual CLI process
└─────────────────┘
```

**Responsibilities**:
- Spawn kiro-cli in PTY with proper environment
- Send messages to CLI stdin
- Read and parse CLI stdout (with ANSI stripping)
- Detect response completion (prompt detection)
- Handle process lifecycle (start, monitor, close)
- Graceful termination (`/exit` command)

**Key Methods**:
```go
Start(ctx)                              // Initialize Kiro CLI process
SendMessage(ctx, msg, handler)          // Send message and stream response
IsRunning()                             // Check process health
Close()                                 // Gracefully terminate
```

**PTY Configuration**:
```go
Environment:
  TERM=xterm-256color
  COLORTERM=truecolor
  Q_TERM=<version>  // Kiro terminal integration

Terminal Size:
  Rows: 40
  Cols: 120
```

**Response Reading Flow**:
```
Send message + "\n" to PTY stdin
    │
    ▼
Read PTY stdout line-by-line
    │
    ├─▶ Parse with OutputParser
    │   ├─▶ Strip ANSI codes
    │   ├─▶ Detect prompt (IsComplete)
    │   └─▶ Extract clean text
    │
    ├─▶ Call ResponseHandler(chunk, isComplete=false)
    │
    ├─▶ Check for completion
    │   ├─▶ Prompt detected ──────────▶ Return with isComplete=true
    │   ├─▶ Timeout (120s) ───────────▶ Return with accumulated output
    │   └─▶ Silence (5s no output) ───▶ Return with accumulated output
    │
    └─▶ Continue reading
```

### 4. Output Parser

**Location**: `internal/kiro/output_parser.go`

The Output Parser cleans PTY output and detects response completion.

**Responsibilities**:
- Strip ANSI escape codes (colors, cursor movements)
- Remove Kiro CLI prompts
- Detect command completion
- Extract clean text for display

**ANSI Code Removal**:
```go
// Removes sequences like:
\x1b[31m          // Color codes
\x1b[2J           // Clear screen
\x1b[H            // Cursor home
\x1b[?25l         // Hide cursor
```

**Prompt Detection**:
```go
Common prompts detected:
  "kiro>"
  "assistant>"
  "> "
  "$ "
```

**Completion Detection Strategy**:
1. Look for CLI prompt at end of output
2. Check for silence timeout (5s no new output)
3. Apply global response timeout (120s)

### 5. Streamer

**Location**: `internal/streaming/streamer.go`, `internal/streaming/buffer.go`

The Streamer manages progressive Slack message updates with debouncing to avoid rate limits.

**Architecture**:
```
ResponseHandler(chunk)
    │
    ▼
OutputBuffer.Append(chunk)
    │
    ├─▶ Accumulate in buffer
    │
    ├─▶ Debounce (500ms)
    │
    └─▶ Flush Callback
            │
            ▼
    Streamer.doUpdate(content)
            │
            ▼
    Slack UpdateMessage(content + ✍️)
```

**Responsibilities**:
- Post initial "Thinking..." message
- Accumulate response chunks
- Debounce updates (default 500ms)
- Add streaming indicator (✍️) during updates
- Post final message (without indicator)
- Handle errors gracefully

**State Machine**:
```
[Not Started]
    │
    ▼ Start()
[Started] ──────┐
    │           │
    │ Update()  │
    ▼           │
[Updating] ◀────┘
    │
    ├─▶ Complete() ──▶ [Completed]
    │
    └─▶ Error() ─────▶ [Completed]
```

**Debouncing Strategy**:
```
Time ──────────────────────────────────────────────▶
      │   chunk1   chunk2   chunk3        chunk4
      │     │        │        │              │
      ├─────▼────────▼────────▼──────────────▼─────
      │    [────buffer────]  flush    [buffer]
      │         (500ms)                  │
      └──────────────┼────────────────────┼────────
                     ▼                    ▼
             UpdateMessage(1-3)   UpdateMessage(4)
```

This reduces Slack API calls while maintaining real-time feel.

### 6. SQLite Store

**Location**: `internal/session/sqlite_store.go`

The SQLite Store provides persistent session storage.

**Schema**:
```sql
CREATE TABLE sessions (
    id TEXT PRIMARY KEY,           -- thread_ts (e.g., "1234567890.123456")
    channel_id TEXT NOT NULL,
    user_id TEXT NOT NULL,
    kiro_session_dir TEXT NOT NULL,
    status TEXT NOT NULL,          -- active, processing, error
    created_at INTEGER NOT NULL,   -- Unix timestamp
    last_activity INTEGER NOT NULL, -- Unix timestamp
    metadata TEXT                  -- JSON for future extension
);

CREATE INDEX idx_user_id ON sessions(user_id);
CREATE INDEX idx_last_activity ON sessions(last_activity);
```

**Responsibilities**:
- CRUD operations for sessions
- Query by user, idle time, status
- Transaction support for consistency
- Automatic database initialization

**Key Queries**:
```go
Get(id)                    // SELECT by id
Save(session)              // INSERT or UPDATE
Delete(id)                 // DELETE by id
List()                     // SELECT all
ListByUser(userID)         // SELECT by user_id
ListIdle(since)            // SELECT by last_activity < since
Count()                    // SELECT COUNT(*)
CountByUser(userID)        // SELECT COUNT(*) by user_id
```

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
     │ EventsAPI (app_mention or message.im)
     ▼
┌─────────────────┐
│  Handler        │◀──── Parse & Clean Message
│  (handler.go)   │
└────┬────────────┘
     │ MessageEvent
     ▼
┌─────────────────────────────────┐
│  Message Processor              │
│  (main.go: processMessage)      │
└────┬────────────────────────────┘
     │
     ├──▶ Determine thread_ts (root or thread)
     │
     ├──▶ SessionManager.GetOrCreate()
     │        │
     │        ├─▶ Check session limits
     │        ├─▶ Create session directory
     │        └─▶ Save to SQLite
     │
     ├──▶ UpdateStatus(processing)
     │
     ├──▶ Streamer.Start()
     │        │
     │        └─▶ Post "🤔 Thinking..." to Slack
     │
     ├──▶ Get or create KiroBridge
     │        │
     │        ├─▶ NewProcess(sessionDir)
     │        ├─▶ NewRetryBridge(process)
     │        └─▶ bridge.Start()
     │                │
     │                ├─▶ Spawn kiro-cli in PTY
     │                └─▶ Wait for initial prompt
     │
     ├──▶ bridge.SendMessage(msg, ResponseHandler)
     │        │
     │        ├─▶ Write msg + "\n" to PTY stdin
     │        │
     │        └─▶ Read PTY stdout
     │                │
     │                └─▶ For each chunk:
     │                        │
     │                        ├─▶ Parser.Parse(chunk)
     │                        │        │
     │                        │        ├─▶ Strip ANSI
     │                        │        └─▶ Detect completion
     │                        │
     │                        └─▶ ResponseHandler(cleanText, isComplete)
     │                                │
     │                                └─▶ Streamer.Update(cleanText)
     │                                        │
     │                                        └─▶ Buffer.Append()
     │                                                │
     │                                                └─▶ Debounce (500ms)
     │                                                        │
     │                                                        └─▶ Slack UpdateMessage()
     │
     ├──▶ Streamer.Complete(finalResponse)
     │        │
     │        └─▶ Slack UpdateMessage (no indicator)
     │
     └──▶ UpdateStatus(active)
```

### Error Handling Flow

```
SendMessage() Error
    │
    ▼
RetryBridge.SendMessage()
    │
    ├─▶ Attempt 1 (initial) ────▶ Success ──▶ Return
    │                              │
    │                              ▼
    │                          Log warning
    │                              │
    │                              ▼
    ├─▶ Attempt 2 (retry) ──────▶ Success ──▶ Return
    │                              │
    │                              ▼
    └─▶ Failure after retries
            │
            ▼
    Streamer.Error(err)
            │
            ├─▶ UpdateMessage("❌ Error: ...")
            │
            ├─▶ Remove bridge from cache
            │
            └─▶ Close bridge
```

## Session Lifecycle

### Creation

```
User sends message in new thread
    │
    ▼
SessionManager.GetOrCreate()
    │
    ├─▶ Check if session exists (by thread_ts)
    │   └─▶ Yes: Update activity, return existing
    │
    ├─▶ Check user session limit (max 5)
    │   └─▶ Exceeded: Return error
    │
    ├─▶ Check total session limit (max 100)
    │   └─▶ Exceeded: Return error
    │
    ├─▶ Create session directory: /tmp/kiro-sessions/{thread_ts}
    │
    ├─▶ Create Session object
    │       ChannelID:      "C1234..."
    │       ThreadTS:       "1234567890.123456"
    │       UserID:         "U1234..."
    │       KiroSessionDir: "/tmp/kiro-sessions/1234567890.123456"
    │       Status:         active
    │       CreatedAt:      now
    │       LastActivity:   now
    │
    └─▶ Save to SQLite
```

### Active Session

```
User sends message in existing thread
    │
    ▼
SessionManager.GetOrCreate()
    │
    └─▶ Session exists
            │
            ├─▶ UpdateActivity()
            │       LastActivity = now
            │
            └─▶ Return existing session
```

### Cleanup

```
Cleanup Goroutine (every 5 minutes)
    │
    ▼
SessionManager.Cleanup()
    │
    ├─▶ ListIdle(lastActivity < 30 minutes ago)
    │
    └─▶ For each idle session:
            │
            ├─▶ Skip if status == processing
            │
            ├─▶ Close(sessionID)
            │       │
            │       ├─▶ Get session from store
            │       │
            │       ├─▶ Remove directory: rm -rf /tmp/kiro-sessions/{thread_ts}
            │       │
            │       └─▶ Delete from SQLite
            │
            └─▶ Log cleanup
```

### Shutdown

```
SIGTERM/SIGINT received
    │
    ▼
Cancel context
    │
    ├─▶ Stop Session Manager
    │       │
    │       └─▶ Stop cleanup goroutine
    │
    ├─▶ Close all bridges
    │       │
    │       └─▶ For each active bridge:
    │               │
    │               ├─▶ Send "/exit\n" to PTY
    │               ├─▶ Wait 500ms
    │               ├─▶ Close PTY
    │               └─▶ Kill process
    │
    └─▶ Close SQLite store
```

## Streaming Architecture

### Why Streaming?

Kiro CLI responses can take 10-120 seconds. Without streaming:
- User sees "Thinking..." for the entire duration
- Poor user experience
- No feedback on progress

With streaming:
- Real-time progressive updates
- User sees response being built
- Better engagement

### Implementation

**OutputBuffer** (`internal/streaming/buffer.go`):
```go
type OutputBuffer struct {
    content      string
    mu           sync.Mutex
    timer        *time.Timer
    flushFunc    func(string) error
    interval     time.Duration
}

func (b *OutputBuffer) Append(chunk string) error {
    b.mu.Lock()
    defer b.mu.Unlock()

    b.content = chunk  // Replace with latest

    if b.timer != nil {
        b.timer.Stop()
    }

    // Schedule flush after interval
    b.timer = time.AfterFunc(b.interval, func() {
        b.flush()
    })
}

func (b *OutputBuffer) flush() {
    b.mu.Lock()
    content := b.content
    b.mu.Unlock()

    b.flushFunc(content)  // Update Slack message
}
```

**Key Properties**:
- **Debouncing**: Accumulates rapid updates, flushes after quiet period
- **Latest wins**: Always shows the latest full response (not incremental)
- **Rate limit friendly**: Max 1 update per 500ms (configurable)

### Slack Rate Limits

Slack API limits:
- Tier 3 methods (chat.postMessage): 50/minute
- Tier 2 methods (chat.update): 100/minute

With debouncing:
- Worst case: 120 updates/minute (2/second)
- Actual: ~5-10 updates per response
- Well under rate limits

## Error Handling

### Levels of Error Handling

1. **Retry Logic** (RetryBridge)
   - Automatic retry once on failure
   - Logs warning on first failure
   - Returns error after max retries

2. **Graceful Degradation** (Streamer)
   - Shows partial response on timeout
   - Posts error message to user
   - Cleans up resources

3. **Resource Cleanup**
   - Remove failed bridges from cache
   - Close PTY and kill process
   - Keep session for future retries (unless explicitly closed)

4. **User Feedback**
   - Clear error messages in Slack
   - Error emoji (❌) for visibility
   - Detailed logs for debugging

### Common Error Scenarios

#### Kiro CLI Not Found
```
Error: exec: "kiro-cli": executable file not found in $PATH
Action: Check kiro.binary_path in config
Result: Error message to user, session preserved
```

#### Kiro CLI Timeout
```
Error: Response timeout after 120s
Action: Return accumulated output as "complete"
Result: Partial response shown to user
```

#### PTY Failure
```
Error: failed to start PTY: permission denied
Action: Log error, retry once, then fail
Result: Error message to user
```

#### Slack API Error
```
Error: slack: update_message: message_not_found
Action: Log error, continue processing
Result: Response lost, but Kiro session continues
```

## Data Persistence

### Session Database

**File**: SQLite database at configured path (default: `/tmp/kiro-agent/sessions.db`)

**Purpose**:
- Survive process restarts
- Track active sessions across deployments
- Enable cleanup on startup (orphaned sessions)

**Migration Strategy**:
- Database created on first run
- Schema version tracking (future)
- Indexes for performance

### Session Directories

**Location**: `{kiro.session_base_path}/{thread_ts}`

**Example**: `/tmp/kiro-sessions/1234567890.123456/`

**Contents**:
- Kiro CLI working directory
- Conversation history (managed by Kiro)
- Any artifacts created by Kiro

**Cleanup**:
- Removed when session is closed
- Automatically cleaned on session timeout

## Concurrency Model

### Thread Safety

1. **Session Manager**
   - Uses `sync.RWMutex` for store access
   - Read lock for queries
   - Write lock for modifications

2. **Bridge Cache**
   - Uses `sync.RWMutex` for bridge map
   - Read lock for lookups
   - Write lock for add/remove

3. **Streamer**
   - Uses `sync.Mutex` for state
   - Protects started/completed flags
   - Prevents concurrent updates

4. **Output Buffer**
   - Uses `sync.Mutex` for content
   - Thread-safe Append()
   - Timer safety with proper cancellation

### Goroutine Architecture

```
Main Goroutine
    │
    ├──▶ Socket Mode Event Loop (1 goroutine)
    │        │
    │        └──▶ Per-event handler (N goroutines, async)
    │                │
    │                └──▶ processMessage() for each message
    │
    ├──▶ Cleanup Loop (1 goroutine)
    │        │
    │        └──▶ Runs every 5 minutes
    │
    └──▶ Signal Handler (1 goroutine)
             │
             └──▶ Graceful shutdown on SIGTERM/SIGINT
```

### Message Processing Concurrency

Each message is processed in its own goroutine:
```go
go func() {
    ctx := context.Background()
    if err := h.messageHandler(ctx, msg); err != nil {
        logger.Error("failed to process message", zap.Error(err))
    }
}()
```

**Benefits**:
- Multiple users can interact concurrently
- One slow response doesn't block others
- Natural load distribution

**Considerations**:
- Session limits prevent resource exhaustion
- Each session has its own Kiro process (isolated)
- Slack API rate limits apply globally

## Performance Characteristics

### Latency

- **Slack → Handler**: <100ms (Socket Mode)
- **Session Lookup**: <5ms (SQLite indexed)
- **Kiro Startup**: 1-3s (first message in thread)
- **Response Time**: 5-120s (depends on query complexity)
- **Streaming Update**: 500ms debounce + API latency

### Resource Usage

- **Memory**: ~10MB base + ~5MB per active session
- **Disk**: ~1MB per session directory
- **CPU**: Low (mostly I/O bound)
- **Database Size**: ~1KB per session

### Scaling Limits

- **Sessions**: Configurable (default 100 total, 5 per user)
- **Kiro Processes**: One per active session
- **Slack Connections**: Single Socket Mode connection
- **Database**: SQLite (suitable for 1000s of sessions)

For higher scale:
- Use PostgreSQL instead of SQLite
- Deploy multiple instances (stateless design)
- Add load balancer for Socket Mode connections
- Implement distributed session store (Redis)

## Future Enhancements

### Potential Improvements

1. **Web Observer**
   - WebSocket-based terminal observation
   - xterm.js frontend for real-time PTY viewing
   - Multi-user observation support

2. **Session Sharing**
   - Allow multiple users to join a session
   - Collaborative AI interactions
   - Permission management

3. **Rich Media**
   - Image generation and display
   - File uploads and downloads
   - Code execution results

4. **Analytics**
   - Usage metrics per user/channel
   - Response time tracking
   - Error rate monitoring

5. **Advanced Routing**
   - Multiple Kiro agents (different models)
   - Route by channel/user/keywords
   - Fallback strategies

## Troubleshooting

### Enable Debug Logging

```yaml
logging:
  level: "debug"
  format: "console"
```

### Inspect Sessions

```bash
sqlite3 /tmp/kiro-agent/sessions.db
> SELECT * FROM sessions;
> SELECT * FROM sessions WHERE last_activity < strftime('%s', 'now') - 1800;
```

### Monitor PTY Output

Add logging to `internal/kiro/process.go`:
```go
p.logger.Debug("pty output", zap.String("raw", string(buf[:n])))
```

### Test Kiro CLI Standalone

```bash
cd /tmp/kiro-sessions/test-session
kiro-cli
> help
```

### Verify Slack Permissions

```bash
curl -H "Authorization: Bearer xoxb-..." \
  https://slack.com/api/auth.test
```

## References

- [Slack Socket Mode Documentation](https://api.slack.com/apis/connections/socket)
- [slack-go/slack Library](https://github.com/slack-go/slack)
- [creack/pty Library](https://github.com/creack/pty)
- [Kiro CLI Documentation](https://github.com/kiro-cli)
