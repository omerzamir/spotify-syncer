#!/bin/bash
# Cron wrapper script for spotify-syncer
# This script handles environment setup and runs the syncer in fast mode

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
BINARY_PATH="$PROJECT_DIR/bin/syncer"
LOG_DIR="$PROJECT_DIR/logs"
LOG_FILE="$LOG_DIR/syncer-cron.log"

cd "$PROJECT_DIR"

if [ ! -f .env ]; then
    echo "$(date '+%Y-%m-%d %H:%M:%S') ERROR: .env file not found in $PROJECT_DIR" >&2
    exit 1
fi

export $(grep -v '^#' .env | grep -v '^$' | xargs)

if [ -z "${SPOTIFY_CLIENT_ID:-}" ] || [ "$SPOTIFY_CLIENT_ID" = "id" ]; then
    echo "$(date '+%Y-%m-%d %H:%M:%S') ERROR: SPOTIFY_CLIENT_ID is not set or is placeholder" >&2
    exit 1
fi

if [ -z "${SPOTIFY_CLIENT_SECRET:-}" ] || [ "$SPOTIFY_CLIENT_SECRET" = "secret" ]; then
    echo "$(date '+%Y-%m-%d %H:%M:%S') ERROR: SPOTIFY_CLIENT_SECRET is not set or is placeholder" >&2
    exit 1
fi

if [ ! -f "$BINARY_PATH" ]; then
    echo "$(date '+%Y-%m-%d %H:%M:%S') Building syncer binary..." >> "$LOG_FILE" 2>&1
    cd "$PROJECT_DIR"
    make build >> "$LOG_FILE" 2>&1 || {
        echo "$(date '+%Y-%m-%d %H:%M:%S') ERROR: Failed to build syncer binary" >> "$LOG_FILE" 2>&1
        exit 1
    }
fi

mkdir -p "$LOG_DIR"

echo "$(date '+%Y-%m-%d %H:%M:%S') Starting syncer run (fast mode)..." >> "$LOG_FILE" 2>&1

"$BINARY_PATH" run --mode=fast >> "$LOG_FILE" 2>&1
EXIT_CODE=$?

if [ $EXIT_CODE -eq 0 ]; then
    echo "$(date '+%Y-%m-%d %H:%M:%S') Syncer run completed successfully" >> "$LOG_FILE" 2>&1
else
    echo "$(date '+%Y-%m-%d %H:%M:%S') ERROR: Syncer run failed with exit code $EXIT_CODE" >> "$LOG_FILE" 2>&1
fi

exit $EXIT_CODE
