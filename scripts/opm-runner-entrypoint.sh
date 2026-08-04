#!/bin/sh
# Legacy shell entrypoint — production images use the opm-runner Go binary.
# Kept for local debugging without rebuilding the binary.
set -eu
action="${OPM_ACTION:-none}"
run_id="${OPM_RUN_ID:-none}"
spec_id="${OPM_SPEC_ID:-}"
out_dir="${OPM_OUT_DIR:-/out}"
mkdir -p "$out_dir" 2>/dev/null || true
if [ -z "${OPM_MODEL_API_KEY:-}" ]; then
  cat > "$out_dir/result.json" <<JSON
{"mode":"fallback","reason":"OPM_MODEL_API_KEY not set; control plane will use builtin artifact helpers","action":"${action}"}
JSON
  cat "$out_dir/result.json"
  exit 0
fi
echo "opm-runner-entrypoint.sh: model key present but shell stub cannot call the API; rebuild image with opm-runner binary" >&2
exit 1
