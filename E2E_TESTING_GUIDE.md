# E2E Testing Quick Start Guide

## Overview

This project includes a comprehensive End-to-End (E2E) test harness that tests the Slack bot against a real Slack workspace. The tests are designed to verify complete functionality from user message to bot response.

## Files Created

### Test Files
- `/internal/e2e/e2e_test.go` - Main E2E test suite (452 lines)
- `/internal/e2e/helpers.go` - Test helper utilities (331 lines)
- `/internal/e2e/README.md` - Detailed E2E testing documentation

### Configuration Files
- `.env.e2e.example` - Template for environment variables
- `scripts/run-e2e-tests.sh` - Helper script to run E2E tests with validation
- `.github/workflows/e2e-tests.yml.example` - CI/CD workflow template

### Updated Files
- `.gitignore` - Added `.env.e2e` and E2E test artifacts
- `Makefile` - Already had `test-e2e` target (verified)

## Quick Start

### 1. Set Up Environment Variables

Copy the example file and fill in your values:

```bash
cp .env.e2e.example .env.e2e
```

Edit `.env.e2e` with your Slack workspace details:

```bash
E2E_TEST=true
TEST_CHANNEL_ID=C1234567890        # Your test channel ID
BOT_USER_ID=U1234567890            # Your bot's user ID
SLACK_BOT_TOKEN=xoxb-your-token    # Your bot token
SLACK_APP_TOKEN=xapp-your-token    # Your app token
```

### 2. Start the Bot

In one terminal, start the bot:

```bash
make run
```

Wait for the bot to connect to Slack.

### 3. Run E2E Tests

In another terminal, run the tests:

```bash
# Load environment variables
source .env.e2e

# Run all E2E tests
make test-e2e

# Or use the helper script (includes validation)
./scripts/run-e2e-tests.sh

# Run a specific test
./scripts/run-e2e-tests.sh TestE2E_MentionAndReply
```

## Test Cases

The E2E test suite includes 8 comprehensive test cases:

1. **TestE2E_MentionAndReply** - Basic bot mention and reply
2. **TestE2E_ThreadContinuation** - Multi-turn conversation in threads
3. **TestE2E_StreamingUpdates** - Progressive message streaming
4. **TestE2E_MultipleThreads** - Concurrent thread handling
5. **TestE2E_ErrorHandling** - Error handling and edge cases
6. **TestE2E_LongConversation** - Extended multi-turn conversations
7. **TestE2E_SpecialCharacters** - Special characters and emojis
8. **TestE2E_CodeBlock** - Code block handling

## Getting Required Values

### Bot User ID

```bash
# Using Slack API
curl -X POST https://slack.com/api/auth.test \
  -H "Authorization: Bearer xoxb-YOUR-BOT-TOKEN" \
  | jq -r '.user_id'
```

### Test Channel ID

1. Open channel in Slack
2. Right-click channel name > "View channel details"
3. Copy the channel ID from the "About" section

Or from the URL: `https://app.slack.com/client/T.../C123456789`
The `C123456789` is your channel ID.

### Tokens

From https://api.slack.com/apps:
- **Bot Token**: OAuth & Permissions > Bot User OAuth Token (xoxb-...)
- **App Token**: Basic Information > App-Level Tokens (xapp-...)

## Required Bot Permissions

Ensure your bot has these OAuth scopes:

- `app_mentions:read` - Receive @mentions
- `channels:history` - Read channel history
- `channels:read` - Read channel info
- `chat:write` - Post messages
- `im:history` - Read DM history
- `im:read` - Read DM info
- `im:write` - Send DMs

## Build Tags

The E2E tests use Go build tags to prevent them from running during normal test execution:

```go
//go:build e2e
// +build e2e
```

This means:
- Regular `go test ./...` will NOT run E2E tests
- You must use `-tags=e2e` to include them
- The Makefile `test-e2e` target handles this automatically

## Test Features

### Environment Validation
Tests check for required environment variables and skip if not configured.

### Automatic Cleanup
Tests clean up messages after execution to keep the test channel tidy.

### Configurable Timeouts
Default timeout is 120 seconds for bot responses, configurable per test.

### Polling Strategy
Tests poll Slack API to detect bot responses with 2-second intervals.

### Helper Utilities
Rich helper functions in `helpers.go` for common operations:
- Post messages and mentions
- Wait for bot replies
- Monitor message updates
- Verify thread structure
- Retry with backoff

## Troubleshooting

### Tests Skip Immediately

**Problem**: All tests show "SKIP: E2E_TEST is not set to 'true'"

**Solution**: Set the E2E_TEST environment variable:
```bash
export E2E_TEST=true
```

### Tests Timeout

**Problem**: Tests timeout waiting for bot response

**Solutions**:
1. Verify bot is running: `ps aux | grep kiro`
2. Check bot is in the test channel
3. Verify tokens are correct
4. Check bot logs for errors

### Permission Errors

**Problem**: Slack API returns permission errors

**Solution**: Add required OAuth scopes in Slack App settings, then reinstall the app.

### Rate Limiting

**Problem**: Tests fail with "rate_limited" errors

**Solutions**:
1. Add delays between tests
2. Use a test workspace with higher limits
3. Run tests less frequently

## CI/CD Integration

For GitHub Actions:

1. Copy the example workflow:
```bash
cp .github/workflows/e2e-tests.yml.example .github/workflows/e2e-tests.yml
```

2. Add secrets to your GitHub repository:
   - `E2E_SLACK_BOT_TOKEN`
   - `E2E_SLACK_APP_TOKEN`
   - `E2E_TEST_CHANNEL_ID`
   - `E2E_BOT_USER_ID`

3. The workflow will run on schedule or manual trigger

## Best Practices

1. **Use a dedicated test channel** - Don't run E2E tests in production channels

2. **Run sparingly** - E2E tests hit real APIs; run them on-demand or scheduled (not on every commit)

3. **Monitor cleanup** - Check that test messages are being deleted properly

4. **Rotate tokens** - Use separate tokens for testing and rotate them regularly

5. **Handle flakiness** - E2E tests can be flaky due to network/timing; consider retries

6. **Dedicated workspace** - Consider using a separate Slack workspace for testing

## Statistics

- **Total E2E test code**: 783 lines (Go)
- **Test cases**: 8
- **Helper functions**: 20+
- **Build tags**: Yes (prevents accidental runs)
- **Automatic cleanup**: Yes
- **CI/CD ready**: Yes

## Support

For issues:
- See detailed docs: `/internal/e2e/README.md`
- Check Slack API docs: https://api.slack.com/
- Review bot logs for debugging

## Next Steps

After running E2E tests successfully:

1. Add E2E tests to your CI/CD pipeline (optional)
2. Create additional test cases for specific features
3. Set up monitoring/alerts for E2E test failures
4. Document any workspace-specific setup requirements
