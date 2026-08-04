#!/bin/sh
# Ephemeral task-automation runner — one container per OPM job phase.
# Future: invoke model-backed agents here. Today: prove spawn and exit 0.
set -eu
action="${OPM_ACTION:-none}"
run_id="${OPM_RUN_ID:-none}"
spec_id="${OPM_SPEC_ID:-}"
echo "opm-runner-task: action=${action} runId=${run_id} specId=${spec_id}"
exit 0
