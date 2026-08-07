# Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `LISTEN_ADDR` | `:8096` | API listen address |
| `HTTP_ADDR` | — | Fallback if `LISTEN_ADDR` unset |
| `ORCHESTRATOR_LISTEN_ADDR` | `:8099` | Orchestrator listen address |
| `OPM_DATA_DIR` | `~/.config/opm` | Linked-project registry + board/task data |
| `OPM_JOB_TMP` | `/tmp/opm-jobs` (laptop) / host bind on NAS | Job workspace root. Per-run scratch under `{runId}/`; **task sessions** persist under `tasks/{projectId}/{specId}/repo` through coding and review until deliver / human gate / done. On NAS the path must be the **same absolute host path** inside and outside `opm-api` so `docker run -v` can mount clones into `opm-runner-task`. |
| `OPM_RUNNER_TAG` | `smoke` (NAS: `nas`) | Tag for `opm-runner-task` image name — prefer `nas` on NAS, never smoke |
| `OPM_FORCE_BUILTIN` | empty | When `1`/`true`, skip `docker run` and use the in-process artifact writer only |
| `OPM_INSTANCE` | `default` | Label value for `opm.instance` on runner containers |
| `PEER_OAM_URL` | empty | **OAM** base URL (required for model-backed jobs). Each job resolves model **and** API key from OAM (`creds:resolve`). Host `OPM_MODEL*` / `CURSOR_API_KEY` are not used as credentials. |
| `OPM_DELIVERY_BRANCH_PREFIX` | `opm` | Branch namespace for delivered task branches (`<prefix>/<specId>`). Empty means no prefix. |
| `OPM_DELIVERY_MAX_FILES` | `50` | Maximum files a single delivery may commit. |
| `OPM_DELIVERY_MAX_BYTES` | `2097152` | Maximum total change-set bytes a single delivery may commit. |
| `OPM_IMPL_AUTO_CHAIN` | `1` | When truthy, a successful `run-implementation` enqueues the next pending coding subtask (same task workspace), or parks the task in `review` for human approve/deliver when coding is complete. Set `0`/`false` to require manual re-enqueue of coding steps. |
| `OPM_IMPL_AUTO_DELIVER` | `1` | Reserved; delivery is human-triggered via `POST …/deliver` after coding parks in `review`. |
| `OPM_GIT_AUTHOR_NAME` | `opm-api` | Committer name on delivery commits. |
| `OPM_GIT_AUTHOR_EMAIL` | `opm-api@localhost` | Committer email on delivery commits. |
| `OPM_RUNNER_NETWORK` | `bridge` | Docker network used when a model key is present (replaces hardened `none` so the runner can reach the model API). |
| `JWT_SECRET` | ephemeral | User JWT secret (≥32 bytes when auth required); share with hub in co-deployed mode |
| `AUTH_MODE` | — | `codeployed` when hub issues tokens |
| `OPA_AUTH_REQUIRED` | off | When `true`/`1`, require user JWT on control-plane routes |
| `OPEN_SERVICE_JWT_SECRET` | empty | Service JWT secret for peer calls |
| `PEER_OPA_URL` | empty | **OPA-Hub** base URL (identity + org directory) |
| `PEER_ORA_URL` | empty | **ORA** base URL (GitHub SCM protocol: list connectors, clone, PR) |
| `PEER_OSA_URL` | empty | Reserved; unused by OPM today |
| `PEER_OPL_URL` | empty | Reserved; unused by OPM today |
| `OPM_PUBLIC_URL` | empty | Public base URL for this product (deep-links) |

## Models and API keys

**Required:** set `PEER_OAM_URL`. Configure providers and per-agent bindings in the OAM console (Agents & Models). Jobs fail closed with `credential_unavailable` when the org has no key. Host `OPM_MODEL*` / `CURSOR_API_KEY` are not credentials — the control plane injects resolved OAM keys into the runner container env only.

### Runner CLI overrides (`opm-runner-task`)

| Variable | Default | Description |
|----------|---------|-------------|
| `OPM_CURSOR_AGENT_BIN` | `agent` | Cursor Agent CLI binary |
| `OPM_CLAUDE_CODE_BIN` | `claude` | Claude Code CLI binary |
| `OPM_QWEN_CODE_BIN` | `qwen` | Qwen Code CLI binary (`cli_qwen_code` endpoints) |

## GitHub credentials

OPM does **not** take `GITHUB_TOKEN` or GitHub App private keys. Connector / PAT **storage** lives in **OAM** (crypto key must match ORA’s connector secret for migrated ciphertext). GitHub App **protocol** (install, callback, webhooks, short-lived clone/push tokens) runs on **ORA**:

| Variable (on `ora-api`) | Purpose |
|-------------------------|---------|
| `OPA_GITHUB_APP_ID` | GitHub App id |
| `OPA_GITHUB_APP_SLUG` | App slug (install URL) |
| `OPA_GITHUB_APP_PRIVATE_KEY` | App private key PEM |
| `OPA_GITHUB_WEBHOOK_SECRET` | Webhook HMAC |
| `OPA_CONNECTOR_SECRET` | AES-GCM for stored PATs (must match `OAM_SECRET_KEY` when OAM holds ciphertext) |
| `OPA_PUBLIC_URL` / `ORA_PUBLIC_URL` | Callback / webhook public base |

Empty peer URLs disable cross-product discovery; the API still boots.
