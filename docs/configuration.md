# Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `LISTEN_ADDR` | `:8096` | API listen address |
| `HTTP_ADDR` | — | Fallback if `LISTEN_ADDR` unset |
| `ORCHESTRATOR_LISTEN_ADDR` | `:8099` | Orchestrator listen address |
| `OPM_DATA_DIR` | `~/.config/opm` | Linked-project registry + board/task data |
| `OPM_JOB_TMP` | `/tmp/opm-jobs` | Ephemeral clone root for job workspaces |
| `OPM_RUNNER_TAG` | `smoke` (NAS: `nas`) | Tag for `opm-runner-task` image name — prefer `nas` on NAS, never smoke |
| `OPM_FORCE_BUILTIN` | empty | When `1`/`true`, skip `docker run` and use the in-process artifact writer only |
| `OPM_INSTANCE` | `default` | Label value for `opm.instance` on runner containers |
| `JWT_SECRET` | ephemeral | User JWT secret (≥32 bytes when auth required); share with hub in co-deployed mode |
| `AUTH_MODE` | — | `codeployed` when hub issues tokens |
| `OPA_AUTH_REQUIRED` | off | When `true`/`1`, require user JWT on control-plane routes |
| `OPEN_SERVICE_JWT_SECRET` | empty | Service JWT secret for peer calls |
| `PEER_OPA_URL` | empty | **OPA-Hub** base URL (identity + org directory) |
| `PEER_ORA_URL` | empty | **ORA** base URL (GitHub connectors + clone credentials) |
| `PEER_OSA_URL` | empty | Optional OSA API base URL |
| `PEER_OPL_URL` | empty | Optional OPL API base URL |
| `OPM_PUBLIC_URL` | empty | Public base URL for this product (deep-links) |

## GitHub credentials

OPM does **not** take `GITHUB_TOKEN` or GitHub App private keys. Configure those on **ORA**:

| Variable (on `ora-api`) | Purpose |
|-------------------------|---------|
| `OPA_GITHUB_APP_ID` | GitHub App id |
| `OPA_GITHUB_APP_SLUG` | App slug (install URL) |
| `OPA_GITHUB_APP_PRIVATE_KEY` | App private key PEM |
| `OPA_GITHUB_WEBHOOK_SECRET` | Webhook HMAC |
| `OPA_CONNECTOR_SECRET` | AES-GCM for stored PATs |
| `OPA_PUBLIC_URL` / `ORA_PUBLIC_URL` | Callback / webhook public base |

Empty peer URLs disable cross-product discovery; the API still boots.
