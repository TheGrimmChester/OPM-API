# Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `LISTEN_ADDR` | `:8096` | API listen address |
| `HTTP_ADDR` | — | Fallback if `LISTEN_ADDR` unset |
| `ORCHESTRATOR_LISTEN_ADDR` | `:8099` | Orchestrator listen address |
| `OPM_DATA_DIR` | `~/.config/opm` | Multi-project registry directory |
| `OPM_RUNNER_TAG` | `smoke` | Tag for `opm-runner-task` image name |
| `JWT_SECRET` | ephemeral | User JWT secret (≥32 bytes when auth required) |
| `OPA_AUTH_REQUIRED` | off | When `true`/`1`, require user JWT on control-plane routes |
| `OPEN_SERVICE_JWT_SECRET` | empty | Service JWT secret for peer probes |
| `PEER_OPA_URL` | empty | Optional OPA hub base URL |
| `PEER_ORA_URL` | empty | Optional ORA API base URL |
| `PEER_OSA_URL` | empty | Optional OSA API base URL |
| `PEER_OPL_URL` | empty | Optional OPL API base URL |
| `OPM_PUBLIC_URL` | empty | Public base URL for this product (deep-links) |

Empty peer URLs disable cross-product features; do not fail boot.
