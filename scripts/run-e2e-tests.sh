#!/bin/bash
#
# Script to run E2E tests with environment validation
# Usage: ./scripts/run-e2e-tests.sh [test-name]
#

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

echo "================================"
echo "E2E Test Runner"
echo "================================"
echo ""

# Check if E2E_TEST is enabled
if [ "$E2E_TEST" != "true" ]; then
    echo "ERROR: E2E_TEST environment variable must be set to 'true'"
    echo ""
    echo "To enable E2E tests, either:"
    echo "  1. Export the variable: export E2E_TEST=true"
    echo "  2. Load .env.e2e file: source .env.e2e"
    echo "  3. Run with the variable: E2E_TEST=true $0"
    echo ""
    exit 1
fi

# Validate required environment variables
REQUIRED_VARS=(
    "TEST_CHANNEL_ID"
    "BOT_USER_ID"
    "SLACK_BOT_TOKEN"
    "SLACK_APP_TOKEN"
)

MISSING_VARS=()
for var in "${REQUIRED_VARS[@]}"; do
    if [ -z "${!var}" ]; then
        MISSING_VARS+=("$var")
    fi
done

if [ ${#MISSING_VARS[@]} -gt 0 ]; then
    echo "ERROR: Missing required environment variables:"
    for var in "${MISSING_VARS[@]}"; do
        echo "  - $var"
    done
    echo ""
    echo "Please set these variables or create a .env.e2e file."
    echo "See .env.e2e.example for a template."
    echo ""
    exit 1
fi

# Validate token formats
if [[ ! "$SLACK_BOT_TOKEN" =~ ^xoxb- ]]; then
    echo "ERROR: SLACK_BOT_TOKEN must start with 'xoxb-'"
    exit 1
fi

if [[ ! "$SLACK_APP_TOKEN" =~ ^xapp- ]]; then
    echo "ERROR: SLACK_APP_TOKEN must start with 'xapp-'"
    exit 1
fi

# Validate channel and user ID formats
if [[ ! "$TEST_CHANNEL_ID" =~ ^[CDG][A-Z0-9]+ ]]; then
    echo "WARNING: TEST_CHANNEL_ID should start with C, D, or G"
fi

if [[ ! "$BOT_USER_ID" =~ ^[UW][A-Z0-9]+ ]]; then
    echo "WARNING: BOT_USER_ID should start with U or W"
fi

# Display configuration
echo "Configuration:"
echo "  TEST_CHANNEL_ID: $TEST_CHANNEL_ID"
echo "  BOT_USER_ID: $BOT_USER_ID"
echo "  SLACK_BOT_TOKEN: ${SLACK_BOT_TOKEN:0:10}..."
echo "  SLACK_APP_TOKEN: ${SLACK_APP_TOKEN:0:10}..."
echo ""

# Check if bot is running
echo "Checking if bot is running..."
if ! pgrep -f "kiro.*slack" > /dev/null 2>&1; then
    echo "WARNING: Bot doesn't appear to be running!"
    echo "Please start the bot in another terminal with: make run"
    echo ""
    read -p "Continue anyway? (y/N) " -n 1 -r
    echo
    if [[ ! $REPLY =~ ^[Yy]$ ]]; then
        exit 1
    fi
else
    echo "Bot appears to be running ✓"
fi

echo ""
echo "Running E2E tests..."
echo "================================"
echo ""

cd "$PROJECT_ROOT"

# Run specific test or all tests
if [ -n "$1" ]; then
    echo "Running test: $1"
    go test -v -tags=e2e -timeout=10m -run "$1" ./internal/e2e
else
    echo "Running all E2E tests"
    go test -v -tags=e2e -timeout=20m ./internal/e2e
fi

TEST_EXIT_CODE=$?

echo ""
echo "================================"
if [ $TEST_EXIT_CODE -eq 0 ]; then
    echo "E2E tests completed successfully! ✓"
else
    echo "E2E tests failed with exit code $TEST_EXIT_CODE"
fi
echo "================================"

exit $TEST_EXIT_CODE
