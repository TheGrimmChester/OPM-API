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

| `OPM_MODEL_API_KEY` | empty | API key for OpenAI-compatible chat completions inside `opm-runner-task`. **Required for model-backed plan/build/review.** When unset, the runner emits an honest fallback and the control plane writes builtin artifacts. Never commit this value — set it in compose `.env` only. |
| `OPM_MODEL_BASE_URL` | `https://api.openai.com/v1` | Base URL for `/chat/completions` (any OpenAI-compatible provider). |
| `OPM_MODEL` | `gpt-4o-mini` | Default model id for all phases. |
| `OPM_MODEL_PLANNING` | empty | Optional override for planning jobs. |
| `OPM_MODEL_CODING` | empty | Optional override for implementation jobs. |
| `OPM_MODEL_REVIEW` | empty | Optional override for review jobs. |
| `OPM_DELIVERY_BRANCH_PREFIX` | `opm` | Branch namespace for delivered task branches (`<prefix>/<specId>`). Empty means no prefix. |
| `OPM_DELIVERY_MAX_FILES` | `50` | Maximum files a single delivery may commit. |
| `OPM_DELIVERY_MAX_BYTES` | `2097152` | Maximum total change-set bytes a single delivery may commit. |
| `OPM_GIT_AUTHOR_NAME` | `opm-api` | Committer name on delivery commits. |
| `OPM_GIT_AUTHOR_EMAIL` | `opm-api@localhost` | Committer email on delivery commits. |
| `OPM_RUNNER_NETWORK` | `bridge` | Docker network used when a model key is present (replaces hardened `none` so the runner can reach the model API). |
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
