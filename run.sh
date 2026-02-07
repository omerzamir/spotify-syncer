#!/bin/bash
# Helper script wrapper for make run
# Usage: ./run.sh [syncer arguments]
# Example: ./run.sh auth
# Example: ./run.sh run --mode fast

exec make run ARGS="$*"
