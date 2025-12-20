# Amelia Slack Agent

A Slack bot that bridges messages to the Kiro CLI agent, enabling conversational AI interactions directly within Slack. The bot supports direct messages, @mentions, and thread-based conversations with persistent context via beads issue tracking.

## Overview

Amelia Slack Agent provides a seamless interface between Slack and the Kiro CLI, allowing teams to interact with an AI agent directly from their workspace. Each Slack thread maintains its own persistent session with the Kiro CLI, preserving conversation context across multiple messages.

### Key Features

- **Direct Message Support**: Send DMs to the bot for private interactions
- **@Mention Support**: Mention the bot in channels to get help
- **Thread-Based Conversations**: Each thread maintains context via beads issue tracking
- **Async Processing**: Messages are queued and processed by a worker pool for scalability
- **Issue-Driven Context**: Conversation history stored in beads (bd) for persistence
- **Comment Sync**: Agent responses automatically sync from beads to Slack threads
- **Retry Logic**: Automatic retry on Kiro CLI failures for reliability
- **Non-Interactive Execution**: Uses `kiro-cli --no-interactive` for simple, robust integration
- **Restart Resilience**: State persisted in beads labels - survives restarts without duplicate messages
- **Automatic Database Sync**: Beads database synchronized on access to prevent stale data issues
- **Thread History Context**: Worker agents can access previous issues from the same conversation thread
- **Race Condition Protection**: Mutex-protected tracking prevents duplicate message delivery

## Prerequisites

- **Go 1.22+**: Required for building and running the agent
- **kiro-cli**: The Kiro CLI must be installed and accessible in your PATH (or specify the binary path in config)
- **beads (bd)**: Git-backed issue tracker for conversation state management
- **Slack App**: A Slack app configured with Socket Mode and appropriate permissions (see [Slack App Configuration](#slack-app-configuration) below)

### Installing beads (bd)

Beads is the issue tracking system that stores conversation state. Install it using one of these methods:

**Quick Install (macOS/Linux):**
```bash
curl -fsSL https://raw.githubusercontent.com/steveyegge/beads/main/scripts/install.sh | bash
```

**Homebrew (macOS/Linux):**
```bash
brew tap steveyegge/beads
brew install bd
```

**Go Install:**
```bash
go install github.com/steveyegge/beads/cmd/bd@latest
export PATH="$PATH:$HOME/go/bin"
```

**Verify installation:**
```bash
bd --version
```

## Slack App Configuration

This section documents the required Slack App permissions and security considerations.

### Required OAuth Scopes

The bot requires these **Bot Token Scopes** (OAuth & Permissions):

**For Public Channels:**

| Scope | Purpose |
|-------|---------|
| `channels:history` | Read message history in public channels where the bot is invited |
| `channels:read` | View basic channel info and list public channels |
| `chat:write` | Send messages as the bot in channels |
| `app_mentions:read` | Receive events when users @mention the bot |

**For Private Channels (Not Recommended):**

| Scope | Purpose | Risk |
|-------|---------|------|
| `groups:history` | Read message history in private channels | **HIGH RISK** |
| `groups:read` | View basic private channel info | Medium |

> **Security Warning**: `groups:history` is flagged by Slack as a high-risk permission because it grants access to ALL messages in private channels, including historical messages from before the bot was added. Private channels often contain sensitive discussions. Consider using public channels + DMs instead.

### Optional OAuth Scopes

These scopes enable additional features but are **not required** for basic channel operation:

| Scope | Purpose | When Needed |
|-------|---------|-------------|
| `im:history` | Read DM history with the bot | For DM support |
| `im:read` | View basic DM info | For DM support |
| `im:write` | Send DMs to users | For DM support |
| `mpim:history` | Read multi-party DM history | For group DM support |

### Event Subscriptions

Enable **Socket Mode** and subscribe to these events:

| Event | Purpose |
|-------|---------|
| `app_mention` | Triggers when users @mention the bot in channels |
| `message.channels` | Triggers on messages in public channels (for thread replies) |
| `message.groups` | Triggers on messages in private channels (for thread replies) |
| `message.im` | Triggers on direct messages to the bot (optional, for DM support) |

### Security Considerations

**Recommended: Public Channels + Optional DMs**

We recommend configuring the bot for **public channels with optional DM support**. This provides the best balance of security and functionality:

| Mode | Security | Use Case |
|------|----------|----------|
| Public channels | Transparent, auditable | Team collaboration, shared context |
| DMs (optional) | User-initiated, private | Sensitive queries users choose to make private |
| Private channels | **Not recommended** | Requires high-risk `groups:history` permission |

**Why avoid private channels?**
- `groups:history` is a **high-risk permission** that grants access to ALL private channel messages
- Includes historical messages from before the bot was added
- Private channels often contain sensitive HR, financial, or confidential discussions

**Recommended scopes (public channels + DMs):**
- `channels:history` - Read public channel messages (low risk)
- `channels:read` - View public channel info
- `chat:write` - Send messages
- `app_mentions:read` - Receive @mentions
- `im:history` - Read DMs to the bot (user-initiated, medium risk)
- `im:read` - View DM info
- `im:write` - Send DMs (for bot responses)

**Recommended events:**
- `app_mention` - Respond to @mentions in channels
- `message.channels` - Thread replies in public channels
- `message.im` - Direct messages to the bot

**Security benefits of this approach:**
1. **Public channels**: Full transparency and audit trail for team interactions
2. **DMs**: Users explicitly choose what to share privately with the bot
3. **No private channel access**: Avoids high-risk `groups:history` permission

### Setup Steps

1. **Create a Slack App** at https://api.slack.com/apps
2. **Enable Socket Mode** under "Socket Mode" in the sidebar
3. **Generate an App-Level Token** with `connections:write` scope
4. **Add Bot Token Scopes** under "OAuth & Permissions"
5. **Subscribe to Events** under "Event Subscriptions"
6. **Install the App** to your workspace
7. **Copy tokens** to your config:
   - Bot User OAuth Token (`xoxb-...`) → `slack.bot_token`
   - App-Level Token (`xapp-...`) → `slack.app_token`
8. **Invite the bot** to channels where you want it to respond (`/invite @YourBotName`)

## Quick Start

### 1. Install Dependencies

```bash
# Clone the repository
git clone https://github.com/sakthiram/kiro-slack-agent
cd kiro-slack-agent

# Install Go dependencies
make deps
```

### 2. Configure the Application

Create a configuration file from the example:

```bash
cp configs/config.example.yaml configs/config.yaml
```

Edit `configs/config.yaml` with your Slack tokens:

```yaml
slack:
  bot_token: "xoxb-your-bot-token"
  app_token: "xapp-your-app-token"

kiro:
  binary_path: "kiro-cli"  # Or full path if not in PATH
  session_base_path: "/tmp/kiro-sessions"

session:
  database_path: "/tmp/kiro-agent/sessions.db"
```

### 3. Build and Run

```bash
# Build the binary
make build

# Run the server
./bin/server -config configs/config.yaml

# Or run directly without building
make run
```

### 4. Start Chatting

Once the bot is running:

- **In DMs**: Send a direct message to the bot
- **In Channels**: @mention the bot followed by your message
- **In Threads**: Reply to any bot message to continue the conversation in that session

## Configuration

The agent is configured via YAML file or environment variables. Environment variables use the prefix `KIRO_AGENT_` with underscores replacing dots (e.g., `KIRO_AGENT_SLACK_BOT_TOKEN`).

### Configuration Options

#### Slack

```yaml
slack:
  bot_token: "xoxb-..."      # Required: Slack Bot User OAuth Token
  app_token: "xapp-..."      # Required: Slack App-Level Token
  debug_mode: false          # Optional: Enable Slack API debug logging
```

#### Kiro CLI

```yaml
kiro:
  binary_path: "kiro-cli"              # Path to kiro-cli binary
  session_base_path: "/tmp/kiro-sessions"  # Base directory for session data
  startup_timeout: "30s"               # Timeout for Kiro CLI startup
  response_timeout: "120s"             # Timeout for Kiro CLI responses
  max_retries: 1                       # Number of retries on failure
```

#### Session Management

```yaml
session:
  idle_timeout: "30m"           # Cleanup sessions idle for this duration
  max_sessions_total: 100       # Maximum total concurrent sessions
  max_sessions_user: 5          # Maximum sessions per user
  database_path: "/tmp/kiro-agent/sessions.db"  # SQLite database location
```

#### Worker Pool

```yaml
worker:
  pool_size: 3                  # Number of concurrent workers
  poll_interval: "10s"          # How often to poll bd ready
  task_timeout: "5m"            # Max time for kiro-cli execution
  max_retries: 2                # Retry count on failure
  retry_backoff: "30s"          # Wait between retries
```

#### Comment Sync

```yaml
sync:
  sync_interval: "5s"           # How often to sync comments to Slack
  enabled: true                 # Enable/disable comment sync
```

#### Logging

```yaml
logging:
  level: "info"                 # Log level: debug, info, warn, error
  format: "json"                # Log format: json, console
```

## Development

### Building

```bash
make build          # Build binary to bin/server
```

### Testing

```bash
make test           # Run all tests with coverage
make test-integration  # Run integration tests (requires kiro-cli)
```

### Linting

```bash
make lint           # Run golangci-lint
```

### Cleaning

```bash
make clean          # Remove build artifacts and coverage files
```

## Project Structure

```
amelia-slack-agent/
├── cmd/server/            # Main entry point
├── internal/
│   ├── config/           # Configuration management
│   ├── logging/          # Structured logging setup
│   ├── slack/            # Slack API integration
│   │   ├── client.go     # Slack API wrapper
│   │   ├── handler.go    # Event handlers (routes to feature processor)
│   │   └── message.go    # Message parsing and formatting
│   ├── beads/            # Beads (bd) issue tracking integration
│   │   ├── manager.go    # BD CLI wrapper for issue CRUD, labels, sync
│   │   └── types.go      # Issue, Comment, ThreadInfo types
│   ├── processor/        # Message processing
│   │   ├── feature_processor.go  # Creates Feature/Task issues from Slack
│   │   └── message_processor.go  # Legacy synchronous processor
│   ├── queue/            # Task queue system
│   │   ├── queue.go      # Channel-based task queue
│   │   ├── poller.go     # Polls bd ready for tasks
│   │   └── types.go      # TaskWork, TaskResult types
│   ├── worker/           # Worker pool for async processing
│   │   ├── pool.go       # Worker pool management
│   │   ├── worker.go     # Individual worker logic with thread context
│   │   └── runner.go     # KiroRunner (non-interactive CLI)
│   └── sync/             # Comment synchronization
│       ├── syncer.go     # Beads → Slack comment sync with restart recovery
│       └── tracker.go    # Label-based sync state tracking
├── configs/             # Configuration files
├── docs/               # Architecture documentation
└── Makefile           # Build and development commands
```

## User Guide

### Interacting with the Bot

#### Direct Messages

Send a direct message to the bot for a private conversation:

```
User: Help me write a Python script to parse CSV files
Bot: 🤔 Thinking...
Bot: ✍️ Sure! I'll help you create a Python script...
```

#### Channel @Mentions

Mention the bot in any channel where it's a member:

```
User: @AmeliaBot can you explain how to use Docker?
Bot: 🤔 Thinking...
Bot: ✍️ Docker is a containerization platform...
```

#### Thread Continuations

Reply to any bot message to continue the conversation:

```
User: @AmeliaBot write a hello world program
Bot: Here's a simple hello world program...

User: [in thread] Now add command line arguments
Bot: I'll modify it to accept command line arguments...
```

Each thread maintains its own Kiro CLI session, preserving full conversation context.

### Response Indicators

- 📝 **Received! Working on it...**: Bot has received a new message and created a Feature issue
- 👍 **Got it! Processing...**: Bot has received a thread reply and created a Task issue
- ✍️ **[agent]**: Agent response synced from beads to Slack
- ❌ **Error**: Something went wrong (error message follows)

**Note**: All responses appear in the correct thread context. When you @mention the bot or reply to a thread, acknowledgments and responses stay in that thread.

### Session Management

- **Automatic Cleanup**: Sessions idle for 30 minutes (default) are automatically closed
- **Session Limits**: Each user can have up to 5 concurrent sessions (default)
- **Persistence**: Sessions survive bot restarts (stored in SQLite)

### Troubleshooting

#### Bot doesn't respond to DMs

- Check that the bot has the `im:read`, `im:write`, `im:history` scopes
- Verify Socket Mode is enabled in your Slack app settings
- Check logs for connection errors

#### Bot doesn't respond to @mentions

- Check that the bot has the `app_mentions:read` scope
- Verify the bot is a member of the channel
- Ensure Event Subscriptions include `app_mention` events

#### Responses time out

- Increase `kiro.response_timeout` in config
- Check that `kiro-cli` is working correctly (test standalone)
- Review logs for Kiro CLI errors

#### Session errors

- Verify the `session.database_path` directory is writable
- Check that `kiro.session_base_path` exists and is writable
- Review session cleanup logs for errors

#### Duplicate messages or missed comments

- The agent automatically syncs the beads database on startup and user access
- Duplicate messages can occur if multiple agent instances are running - check for zombie processes
- Database sync happens automatically via `bd sync --import-only` - no manual intervention needed
- All read operations use `--allow-stale` flag for resilience against temporary sync issues
- Restart the agent if you suspect stale state - the agent will restore all active conversations automatically

## Architecture

For detailed architecture documentation including component descriptions, message flow, and task processing, see [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md).

### High-Level Flow

```
User Message → Slack → Socket Mode → Handler → Feature Processor
                                                      ↓
                                              Create Feature/Task
                                              in Beads (bd)
                                                      ↓
                                         Poller (bd ready) → Task Queue
                                                      ↓
                                              Worker Pool
                                                      ↓
                                         kiro-cli --no-interactive
                                                      ↓
                                         Add [agent] comment to beads
                                                      ↓
                                         Comment Syncer → Slack Thread
```

The architecture is **fully async**: the Slack handler returns immediately after creating the beads issue, and the response arrives via the comment sync loop.

## State Persistence and Restart Recovery

The agent persists all sync state in beads labels, making it stateless and resilient to restarts:

- **Comment Tracking**: Synced comments tracked via `synced:<comment_id>` labels
- **Issue Registration**: Active issues registered with `thread:<ts>`, `channel:<id>`, `user:<id>`, `msg:<ts>` labels
- **Automatic Restoration**: On startup, the syncer scans beads and re-registers all issues with status `open`, `in_progress`, or `ready`
- **No Duplicate Messages**: Label-based tracking prevents duplicate Slack messages after restarts
- **Database Sync**: Beads database automatically synced on first user access via `bd sync --import-only`

This design ensures the agent can be restarted at any time without losing track of conversations or sending duplicate responses.

### How It Works

1. **On Startup**: The syncer calls `Restore()` which scans all beads issues and re-registers active conversations
2. **During Operation**: Each synced comment gets a `synced:<comment_id>` label to prevent re-posting
3. **On User Access**: `EnsureUserDir()` runs `bd sync --import-only` to sync remote changes
4. **Read Operations**: All beads read commands use `--allow-stale` flag for resilience

### Thread History for Worker Agents

Worker agents processing tasks receive the `thread:` label from the issue, enabling them to query all related issues from the same conversation:

```bash
# Get all issues from the same Slack thread
bd list --label thread:1766187249.573709 --json --allow-stale
```

This allows agents to:
- Review what the user previously asked
- See what analysis was already done
- Build on previous solutions and queries
- Provide consistent, context-aware responses

## Contributing

Contributions are welcome! Please follow these guidelines:

1. Fork the repository
2. Create a feature branch (`git checkout -b feature/amazing-feature`)
3. Write tests for new functionality
4. Ensure tests pass (`make test`)
5. Run linter (`make lint`)
6. Commit your changes
7. Push to your branch
8. Open a Pull Request

## License

This project is licensed under the MIT License - see the LICENSE file for details.

## Support

For issues, questions, or contributions:

- Open an issue on GitHub
- Check existing documentation in the `docs/` directory
- Review logs with `logging.level: "debug"` for troubleshooting

## Acknowledgments

- Built with [slack-go/slack](https://github.com/slack-go/slack) for Slack integration
- Uses [creack/pty](https://github.com/creack/pty) for PTY management
- Powered by Kiro CLI agent
- Issue tracking via [beads (bd)](https://github.com/steveyegge/beads) - Git-backed issue tracker for AI workflows
