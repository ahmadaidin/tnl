#!/bin/sh
# Fake ssh for tnl supervisor tests.
#
# Every spawn appends the full argv to $FAKE_SSH_LOG (one line per spawn).
# FAKE_SSH_EXIT_IMMEDIATE=1 makes the process exit 255 right after logging,
# simulating a dead connection. Otherwise the process stays alive until it
# receives SIGINT/SIGTERM (the supervisor's graceful kill), then exits 0.
set -u
: "${FAKE_SSH_LOG:?FAKE_SSH_LOG must be set}"
echo "$@" >> "$FAKE_SSH_LOG"
if [ "${FAKE_SSH_EXIT_IMMEDIATE:-0}" = "1" ]; then
    exit 255
fi
trap 'kill "$spid" 2>/dev/null; exit 0' INT TERM
# Redirect the background sleeper so an orphaned one can never hold the
# supervisor's stdout/stderr pipes open (which would block cmd.Wait()).
sleep 3600 >/dev/null 2>&1 &
spid=$!
wait
