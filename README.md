# Amelia Slack Agent

A Slack bot that bridges messages to the Kiro CLI agent, enabling conversational AI interactions directly within Slack. The bot supports direct messages, @mentions, thread-based sessions, and real-time streaming responses.

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

## Prerequisites

- **Go 1.22+**: Required for building and running the agent
- **kiro-cli**: The Kiro CLI must be installed and accessible in your PATH (or specify the binary path in config)
- **Slack App**: A Slack app configured with Socket Mode and appropriate permissions (see [Slack App Configuration](#slack-app-configuration) below)

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

#### Streaming

```yaml
streaming:
  update_interval: "500ms"      # Debounce interval for Slack message updates
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
│   ├── logging/          # Logging setup
│   ├── slack/            # Slack API integration
│   │   ├── client.go     # Slack API wrapper
│   │   ├── handler.go    # Event handlers (routes to feature processor)
│   │   └── message.go    # Message parsing and formatting
│   ├── beads/            # Beads (bd) issue tracking integration
│   │   ├── manager.go    # BD CLI wrapper for issue CRUD
│   │   └── types.go      # Issue, Comment, ThreadInfo types
│   ├── processor/        # Message processing
│   │   ├── feature_processor.go  # Creates Feature/Task issues
│   │   └── message_processor.go  # Legacy synchronous processor
│   ├── queue/            # Task queue system
│   │   ├── queue.go      # Channel-based task queue
│   │   ├── poller.go     # Polls bd ready for tasks
│   │   └── types.go      # TaskWork, TaskResult types
│   ├── worker/           # Worker pool for async processing
│   │   ├── pool.go       # Worker pool management
│   │   ├── worker.go     # Individual worker logic
│   │   └── runner.go     # KiroRunner (non-interactive CLI)
│   ├── sync/             # Comment synchronization
│   │   ├── syncer.go     # Beads → Slack comment sync
│   │   └── tracker.go    # Sync state tracking
│   ├── kiro/             # Kiro CLI integration (legacy PTY)
│   │   └── ...           # Bridge, process, parser (deprecated)
│   └── streaming/        # Streaming updates (legacy)
│       └── ...           # Streamer, buffer
├── configs/             # Configuration files
├── docs/               # Documentation
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

- 🤔 **Thinking...**: Bot has received your message and is processing
- ✍️ **Writing...**: Bot is streaming its response (message updates in real-time)
- ❌ **Error**: Something went wrong (error message follows)

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

## Web Terminal Observer

The Web Terminal Observer allows you to watch Kiro agent sessions in real-time through your browser, similar to `tmux attach`. This is useful for debugging, monitoring, or simply watching how the agent processes requests.

### Enabling the Web Observer

Add the following to your `config.yaml`:

```yaml
web:
  enabled: true
  listen_addr: ":8080"           # HTTP server address
  static_path: "./web/static"    # Path to web UI files
  max_observers_per_session: 10  # Max concurrent viewers per session
```

### Using the Web Observer

1. **Start the server** with web observer enabled
2. **Open your browser** to `http://localhost:8080`
3. **View active sessions** - see all currently active Kiro sessions
4. **Click a session** to attach and watch the terminal output in real-time

### Features

- **Real-time streaming**: See agent output as it happens via WebSocket
- **Scrollback history**: Late joiners see recent output (64KB buffer)
- **Multiple observers**: Multiple people can watch the same session
- **Read-only by default**: Observers can only watch, not interact
- **Session list**: View all active sessions with status and user info

### API Endpoints

| Endpoint | Description |
|----------|-------------|
| `GET /` | Web UI |
| `GET /api/sessions` | List all active sessions (JSON) |
| `GET /api/sessions/:id` | Get session details (JSON) |
| `GET /ws/sessions/:id/stream` | WebSocket for real-time streaming |
| `GET /api/health` | Health check |

### Architecture

```
Browser (xterm.js) <--WebSocket--> Server <--PTY Multiplexer--> kiro-cli
                                     |
                            ObservableProcess
                            (broadcasts to all observers)
```

The `ObservableProcess` wraps the standard Kiro PTY process and broadcasts output to:
1. The Slack streamer (for message updates)
2. All connected web observers (via WebSocket)

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
