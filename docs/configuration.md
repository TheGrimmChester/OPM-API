# Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `LISTEN_ADDR` | `:8096` | API listen address |
| `HTTP_ADDR` | — | Fallback if `LISTEN_ADDR` unset |
| `ORCHESTRATOR_LISTEN_ADDR` | `:8099` | Orchestrator listen address |
| `OPM_DATA_DIR` | `~/.config/opm` | Linked-project registry + board/task data |
| `OPM_JOB_TMP` | `/tmp/opm-jobs` (laptop) / host bind on NAS | Job workspace root. Per-run scratch under `{runId}/`; **task implementation sessions** persist under `tasks/{projectId}/{specId}/repo` until coding completes or review starts. On NAS the path must be the **same absolute host path** inside and outside `opm-api` so `docker run -v` can mount clones into `opm-runner-task`. |
| `OPM_RUNNER_TAG` | `smoke` (NAS: `nas`) | Tag for `opm-runner-task` image name — prefer `nas` on NAS, never smoke |
| `OPM_FORCE_BUILTIN` | empty | When `1`/`true`, skip `docker run` and use the in-process artifact writer only |
| `OPM_INSTANCE` | `default` | Label value for `opm.instance` on runner containers |

| `OPM_MODEL_API_KEY` | empty | API key for Cursor Agent CLI (default) or OpenAI-compatible chat inside `opm-runner-task`. **Required for model-backed plan/build/review/ideation/roadmap.** When unset, the runner emits an honest fallback and the control plane writes builtin artifacts. Never commit this value — set it in compose `.env` only. |
| `OPM_MODEL_BASE_URL` | `https://api.openai.com/v1` | Base URL for `/chat/completions` when `OPM_MODEL_PROVIDER=openai`. |
| `OPM_MODEL` | `auto` | Default model id for all phases. Cursor Agent CLI uses `auto` for now. |
| `OPM_MODEL_PLANNING` | `auto` | Planning jobs (`OPM_MODEL` when unset). |
| `OPM_MODEL_CODING` | `auto` | Implementation jobs (`OPM_MODEL` when unset). |
| `OPM_MODEL_REVIEW` | `auto` | Review jobs (`OPM_MODEL` when unset). |
| `OPM_MODEL_IDEATION` | `auto` | `run-ideation` (`OPM_MODEL` when unset). |
| `OPM_MODEL_ROADMAP_DISCOVERY` | `auto` | `run-roadmap-discovery` (`OPM_MODEL` when unset). |
| `OPM_MODEL_ROADMAP_FEATURES` | `auto` | `run-roadmap-features` (`OPM_MODEL` when unset). |
| `OPM_DELIVERY_BRANCH_PREFIX` | `opm` | Branch namespace for delivered task branches (`<prefix>/<specId>`). Empty means no prefix. |
| `OPM_DELIVERY_MAX_FILES` | `50` | Maximum files a single delivery may commit. |
| `OPM_DELIVERY_MAX_BYTES` | `2097152` | Maximum total change-set bytes a single delivery may commit. |
| `OPM_IMPL_AUTO_CHAIN` | `1` | When truthy, a successful `run-implementation` enqueues the next pending coding subtask automatically (same task workspace). Set `0`/`false` to require manual re-enqueue. |
| `OPM_IMPL_AUTO_DELIVER` | `1` | When truthy and ORA is linked, finishing the last coding subtask auto-runs delivery (commit/push/PR) from the task workspace, then releases it. Set `0`/`false` to deliver manually. |
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
