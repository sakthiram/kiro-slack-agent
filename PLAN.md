# Slack Agent Backend - Kiro CLI Bridge

## Overview
Build a Go backend that bridges Slack messages to Kiro CLI agent. Uses Socket Mode for real-time events, PTY for Kiro interaction, and streaming message updates.

**Key Design Decisions:**
- **Session Model**: Thread timestamp = Session ID. New message starts session; thread replies continue it.
- **Kiro Interface**: PTY-based process per session (Kiro uses directory-based persistence, default agent)
- **Streaming**: Debounced Slack message updates as Kiro responds
- **Error Handling**: Auto-retry once on Kiro failure, then report to user
- **Session Storage**: SQLite for persistence across restarts

---

## Project Structure

```
amelia-slack-agent/
├── cmd/server/main.go              # Entry point
├── internal/
│   ├── config/config.go            # Configuration management
│   ├── slack/
│   │   ├── handler.go              # Event handlers (DM, @mention)
│   │   ├── client.go               # Slack API wrapper
│   │   └── message.go              # Message formatting
│   ├── session/
│   │   ├── manager.go              # Session lifecycle
│   │   ├── session.go              # Session entity
│   │   ├── store.go                # Store interface
│   │   └── sqlite_store.go         # SQLite session store
│   ├── kiro/
│   │   ├── bridge.go               # Kiro interface
│   │   ├── process.go              # PTY process management
│   │   └── output_parser.go        # Clean ANSI, detect completion
│   └── streaming/
│       ├── streamer.go             # Slack message updater
│       └── buffer.go               # Debounced output buffer
├── pkg/testutil/
│   ├── mocks.go                    # Mock interfaces
│   └── fixtures.go                 # Test fixtures
├── configs/config.example.yaml
├── go.mod
├── Makefile
└── README.md
```

---

## Dependencies

```go
require (
    github.com/slack-go/slack v0.12.5      // Slack SDK with Socket Mode
    github.com/creack/pty v1.1.21          // PTY for Kiro CLI
    github.com/spf13/viper v1.18.2         // Configuration
    go.uber.org/zap v1.27.0                // Logging
    github.com/mattn/go-sqlite3 v1.14.22   // SQLite driver
    github.com/stretchr/testify v1.9.0     // Testing
    github.com/golang/mock v1.6.0          // Mocks
)
```

---

## Core Interfaces

### SlackClient
```go
type SlackClient interface {
    PostMessage(ctx, channelID, text string, opts ...Option) (ts string, err error)
    UpdateMessage(ctx, channelID, ts, text string) error
    AddReaction(ctx, channelID, ts, emoji string) error
    RemoveReaction(ctx, channelID, ts, emoji string) error
}
```

### SessionManager
```go
type SessionManager interface {
    GetOrCreate(ctx, channelID, threadTS, userID string) (*Session, isNew bool, err error)
    Get(ctx, id SessionID) (*Session, error)
    UpdateActivity(ctx, id SessionID) error
    Close(ctx, id SessionID) error
    Cleanup(ctx) error
}
```

### KiroBridge
```go
type ResponseHandler func(chunk string, isComplete bool) error

type KiroBridge interface {
    Start(ctx, sessionDir string) error
    SendMessage(ctx, message string, handler ResponseHandler) error
    IsRunning() bool
    Close() error
}
```

### StreamUpdater
```go
type StreamUpdater interface {
    Start(ctx, channelID, threadTS string) (messageTS string, err error)
    Update(ctx, content string) error
    Complete(ctx, finalContent string) error
    Error(ctx, err error) error
}
```

---

## Message Flow

```
User → Slack → [Socket Mode Handler] → [Session Manager] → [Kiro Bridge]
                      ↓                      ↓                   ↓
                   Ack event          Get/Create session    PTY stdin
                      ↓                      ↓                   ↓
               [Streamer.Start]        Map thread_ts      PTY stdout
                      ↓                   to session            ↓
               Post "Thinking..."           ↓            [Output Parser]
                      ↓                      ↓                   ↓
               [Streamer.Update] ← ← ← ResponseHandler(chunk) ←─┘
                      ↓
               Update Slack msg
                      ↓
               [Streamer.Complete]
                      ↓
               Final message
```

**Thread Continuation:**
- Root message: `threadTS = messageTS` (creates session)
- Thread reply: Uses existing `threadTS` (continues session, same Kiro process)

---

## Implementation Phases

### Phase 1: Foundation
1. Initialize Go module, project structure
2. Implement config loading (viper) with validation
3. Setup zap logging
4. Create Makefile (build, test, lint, run)

### Phase 2: Slack Integration
1. Implement SlackClient wrapper
2. Setup Socket Mode connection
3. Register handlers for `app_mention` and DM `message` events
4. Implement message formatting (clean @mentions)
5. **Tests**: Unit tests for client, handler routing

### Phase 3: Session Management
1. Implement Session entity and SessionID (thread_ts)
2. Implement SQLiteStore (sessions table, CRUD operations)
3. Implement SessionManager with GetOrCreate, cleanup goroutine
4. Create session directories for Kiro
5. **Tests**: Unit tests for store (with in-memory SQLite), manager lifecycle

### Phase 4: Kiro Bridge
1. Implement PTY process spawning (`github.com/creack/pty`)
2. Implement output parser (ANSI removal, prompt detection)
3. Implement SendMessage with streaming callback
4. Handle process lifecycle (start, health check, close)
5. **Tests**: Unit tests for parser, integration test with real Kiro

### Phase 5: Streaming Updates
1. Implement OutputBuffer with debouncing
2. Implement StreamUpdater (post, update, complete)
3. Wire streaming to Kiro ResponseHandler
4. Add thinking reaction during processing
5. **Tests**: Unit tests for buffer, streamer

### Phase 6: Integration & E2E Testing
1. Wire all components in main.go
2. Implement auto-retry logic (retry once on Kiro failure)
3. Integration tests (Slack→Session→Kiro flow with mocks)
4. E2E test harness (real Slack test workspace)
5. Documentation (README, config guide)

---

## Testing Strategy

### Unit Tests (per component)
| Component | Test Focus |
|-----------|------------|
| `slack/handler` | Event routing, mention cleaning, error paths |
| `session/manager` | GetOrCreate logic, limits, cleanup |
| `session/store` | CRUD operations, concurrency |
| `kiro/parser` | ANSI removal, prompt detection, edge cases |
| `kiro/process` | Lifecycle (mock PTY) |
| `streaming/buffer` | Debouncing, flush timing |
| `streaming/streamer` | Update flow, completion |

### Integration Tests
1. **Slack→Session**: Real SessionManager + mock SlackClient/KiroBridge
2. **Session→Kiro**: Real Kiro process (skip if not installed)
3. **Full flow**: Mock external deps, test component wiring

### E2E Tests (optional, requires test Slack workspace)
```bash
E2E_TEST=true TEST_CHANNEL_ID=C123 go test ./internal/e2e/...
```
- Post message to test channel
- Verify bot responds in thread
- Verify thread continuation works

---

## Configuration

```yaml
# configs/config.yaml
slack:
  bot_token: "${SLACK_BOT_TOKEN}"   # xoxb-...
  app_token: "${SLACK_APP_TOKEN}"   # xapp-...

kiro:
  binary_path: "kiro-cli"
  session_base_path: "/tmp/kiro-sessions"
  startup_timeout: "30s"
  response_timeout: "120s"
  max_retries: 1                    # Auto-retry once on failure

session:
  idle_timeout: "30m"
  max_sessions_total: 100
  max_sessions_user: 5
  database_path: "/tmp/kiro-agent/sessions.db"  # SQLite database

streaming:
  update_interval: "500ms"
```

---

## Critical Files to Create

| File | Purpose |
|------|---------|
| `cmd/server/main.go` | Entry point, dependency wiring |
| `internal/slack/handler.go` | Core event routing |
| `internal/session/manager.go` | Session lifecycle |
| `internal/session/sqlite_store.go` | SQLite persistence |
| `internal/kiro/process.go` | PTY-based Kiro interaction |
| `internal/kiro/output_parser.go` | Clean Kiro output |
| `internal/streaming/streamer.go` | Slack message updates |

---

## Potential Challenges & Mitigations

1. **Kiro output parsing**: Robust ANSI removal + flexible prompt detection + timeout fallback
2. **Orphaned processes**: Process groups, cleanup on startup, health checks
3. **Slack rate limits**: Debounced updates (500ms default), exponential backoff
4. **Memory with many sessions**: Enforce limits, aggressive idle cleanup
