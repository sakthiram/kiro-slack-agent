# End-to-End (E2E) Tests

This directory contains end-to-end tests for the Slack Kiro Agent that interact with the real Slack API.

## Overview

The E2E tests verify the complete functionality of the bot by:
1. Posting messages to a real Slack workspace
2. Waiting for the bot to respond
3. Verifying the bot's behavior and responses
4. Testing various scenarios (mentions, threads, DMs, etc.)

## Prerequisites

Before running E2E tests, you need:

1. **A Slack workspace with the bot installed**
2. **Bot tokens** (bot token and app token)
3. **A test channel** where the bot can post messages
4. **The bot's user ID**

## Environment Variables

The E2E tests require the following environment variables:

| Variable | Description | Example | Required |
|----------|-------------|---------|----------|
| `E2E_TEST` | Enable E2E tests | `true` | Yes |
| `TEST_CHANNEL_ID` | Channel ID for testing | `C1234567890` | Yes |
| `BOT_USER_ID` | Bot's user ID | `U1234567890` | Yes |
| `SLACK_BOT_TOKEN` | Bot user OAuth token | `xoxb-...` | Yes |
| `SLACK_APP_TOKEN` | App-level token | `xapp-...` | Yes |

### Getting the Required Values

#### 1. Bot Tokens

From your Slack App settings (https://api.slack.com/apps):
- **Bot Token**: OAuth & Permissions > Bot User OAuth Token (starts with `xoxb-`)
- **App Token**: Basic Information > App-Level Tokens (starts with `xapp-`)

#### 2. Bot User ID

You can find the bot's user ID in several ways:

**Option A: From Slack App Settings**
- Go to your app's page at https://api.slack.com/apps
- Click on "OAuth & Permissions"
- The Bot User ID is shown in the "Bot User OAuth Token" section

**Option B: Using the Slack API**
```bash
curl -X POST https://slack.com/api/auth.test \
  -H "Authorization: Bearer xoxb-YOUR-BOT-TOKEN"
```

**Option C: From Slack Client**
- Right-click on the bot in Slack
- Select "View profile"
- Click "More" > "Copy member ID"

#### 3. Test Channel ID

**Option A: From Slack URL**
- Open the channel in Slack web or desktop
- The URL will look like: `https://app.slack.com/client/T.../C123456789`
- The `C123456789` part is the channel ID

**Option B: From Slack Client**
- Right-click on the channel
- Select "View channel details"
- Look for the channel ID in the "About" section

**Option C: Using the Slack API**
```bash
curl -X GET https://slack.com/api/conversations.list \
  -H "Authorization: Bearer xoxb-YOUR-BOT-TOKEN"
```

## Running E2E Tests

### Quick Start

1. **Set up environment variables:**

```bash
export E2E_TEST=true
export TEST_CHANNEL_ID=C1234567890
export BOT_USER_ID=U1234567890
export SLACK_BOT_TOKEN=xoxb-your-bot-token
export SLACK_APP_TOKEN=xapp-your-app-token
```

2. **Ensure the bot is running:**

```bash
# In a separate terminal
make run
# or
go run ./cmd/server
```

3. **Run the E2E tests:**

```bash
make test-e2e
```

### Running Specific Tests

Run a specific test:
```bash
E2E_TEST=true go test -v -tags=e2e ./internal/e2e -run TestE2E_MentionAndReply
```

Run tests with a timeout:
```bash
E2E_TEST=true go test -v -tags=e2e -timeout=10m ./internal/e2e
```

Run tests in short mode (skips long tests):
```bash
E2E_TEST=true go test -v -tags=e2e -short ./internal/e2e
```

### Using an Environment File

Create a `.env.e2e` file:
```bash
E2E_TEST=true
TEST_CHANNEL_ID=C1234567890
BOT_USER_ID=U1234567890
SLACK_BOT_TOKEN=xoxb-your-bot-token
SLACK_APP_TOKEN=xapp-your-app-token
```

Then load it before running tests:
```bash
source .env.e2e
make test-e2e
```

## Test Cases

### TestE2E_MentionAndReply
Tests basic bot mention and reply functionality.
- Posts a message mentioning the bot
- Waits for bot response
- Verifies response is received and non-empty

### TestE2E_ThreadContinuation
Tests that bot continues conversation in threads.
- Posts initial mention to create thread
- Posts follow-up messages in thread
- Verifies bot maintains context and responds to each message

### TestE2E_StreamingUpdates
Tests that bot streams progressive updates.
- Posts a message that requires processing time
- Monitors message updates
- Verifies bot updates message progressively

### TestE2E_MultipleThreads
Tests bot handling multiple concurrent threads.
- Creates multiple separate threads
- Verifies bot responds to each thread independently
- Ensures threads don't interfere with each other

### TestE2E_ErrorHandling
Tests bot behavior with invalid/edge case requests.
- Posts problematic messages (empty, malformed, etc.)
- Verifies bot handles errors gracefully
- Ensures bot provides meaningful error responses

### TestE2E_LongConversation
Tests extended conversation with multiple turns.
- Conducts multi-turn conversation
- Verifies bot maintains context across multiple messages
- Tests conversation memory and continuity

### TestE2E_SpecialCharacters
Tests bot handling of special characters and emojis.
- Posts messages with symbols, emojis, special characters
- Verifies bot processes and responds appropriately

### TestE2E_CodeBlock
Tests bot handling of code blocks.
- Posts messages containing code blocks
- Verifies bot can process and respond to code-related questions

## Troubleshooting

### Tests are skipped
**Problem**: Tests show "Skipping E2E test: E2E_TEST is not set to 'true'"

**Solution**: Ensure `E2E_TEST=true` is set:
```bash
export E2E_TEST=true
```

### Bot not responding
**Problem**: Tests timeout waiting for bot response

**Possible causes**:
1. Bot is not running - start the bot with `make run`
2. Bot is not in the test channel - invite bot to the channel
3. Wrong tokens - verify `SLACK_BOT_TOKEN` and `SLACK_APP_TOKEN`
4. Wrong bot user ID - verify `BOT_USER_ID`

**Debug steps**:
```bash
# Check bot is running
ps aux | grep "kiro.*slack"

# Test bot token
curl -X POST https://slack.com/api/auth.test \
  -H "Authorization: Bearer $SLACK_BOT_TOKEN"

# Check bot is in channel
curl -X GET "https://slack.com/api/conversations.members?channel=$TEST_CHANNEL_ID" \
  -H "Authorization: Bearer $SLACK_BOT_TOKEN"
```

### Permission errors
**Problem**: Tests fail with Slack API permission errors

**Solution**: Verify your bot has the required OAuth scopes:
- `app_mentions:read` - Receive mentions
- `channels:history` - Read channel messages
- `channels:read` - Read channel information
- `chat:write` - Post messages
- `im:history` - Read DM history
- `im:read` - Read DM information
- `im:write` - Send DMs

Update scopes in: Slack App Settings > OAuth & Permissions > Scopes

### Messages not being cleaned up
**Problem**: Test messages remain in the channel

**Solution**: The tests attempt to clean up messages automatically. If cleanup fails:
1. Check bot has `chat:write` permission
2. Manually delete test messages from the channel
3. Consider using a dedicated test channel

### Rate limiting
**Problem**: Tests fail with "rate_limited" errors

**Solution**:
- Add delays between tests
- Use a workspace with higher rate limits
- Run tests less frequently

## Best Practices

1. **Use a dedicated test channel** - Don't run E2E tests in production channels

2. **Run tests in CI sparingly** - E2E tests hit real APIs and should be run less frequently than unit tests

3. **Monitor test messages** - Ensure test messages are being cleaned up properly

4. **Keep tokens secure** - Never commit tokens to version control. Use environment variables or secret management.

5. **Test in isolation** - Don't run multiple E2E test suites simultaneously on the same bot

6. **Handle flakiness** - E2E tests can be flaky due to network issues, timing, etc. Consider retries.

## Security Notes

- Never commit `.env` files with real tokens
- Use separate tokens for testing and production
- Rotate tokens regularly
- Consider using a dedicated test Slack workspace
- Limit test token permissions to minimum required

## Continuous Integration

Example GitHub Actions workflow:

```yaml
name: E2E Tests
on:
  schedule:
    - cron: '0 0 * * *'  # Daily
  workflow_dispatch:  # Manual trigger

jobs:
  e2e:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v3

      - name: Set up Go
        uses: actions/setup-go@v4
        with:
          go-version: '1.23'

      - name: Start Bot
        run: |
          make build
          ./bin/server &
          sleep 10
        env:
          SLACK_BOT_TOKEN: ${{ secrets.SLACK_BOT_TOKEN }}
          SLACK_APP_TOKEN: ${{ secrets.SLACK_APP_TOKEN }}

      - name: Run E2E Tests
        run: make test-e2e
        env:
          E2E_TEST: true
          TEST_CHANNEL_ID: ${{ secrets.TEST_CHANNEL_ID }}
          BOT_USER_ID: ${{ secrets.BOT_USER_ID }}
          SLACK_BOT_TOKEN: ${{ secrets.SLACK_BOT_TOKEN }}
          SLACK_APP_TOKEN: ${{ secrets.SLACK_APP_TOKEN }}
```

## Contributing

When adding new E2E tests:

1. Use the `//go:build e2e` tag at the top of test files
2. Check for `E2E_TEST=true` in `setupE2E()`
3. Clean up test messages in defer statements
4. Use descriptive test names: `TestE2E_<Feature>`
5. Add proper logging with `t.Logf()`
6. Handle timeouts appropriately
7. Document new environment variables
8. Update this README with new test cases

## Support

For issues with:
- **Test framework**: Open an issue in this repository
- **Slack API**: Check [Slack API documentation](https://api.slack.com/)
- **Bot configuration**: Review the main README.md
