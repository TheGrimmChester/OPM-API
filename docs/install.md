# Install

## Local (dev)

Requirements: Go 1.22+, sibling `Open-Auth-Go` and `Open-Job-Go` checkouts (go `replace` directives).

```bash
cd OPM-API
go run .
# health: http://127.0.0.1:8096/api/health
```

Orchestrator stub:

```bash
go run . orchestrator
# health: http://127.0.0.1:8099/api/health
```

## Container images

Build stages in `Dockerfile`:

| Stage | Image name | Tag |
|-------|------------|-----|
| `opm-api` | `opm-api` | `smoke` (laptop) / `nas` (production) |
| `opm-orchestrator` | same binary | command `orchestrator` |
| `opm-runner-task` | `opm-runner-task` | ephemeral only |

```bash
docker build --target opm-api -t opm-api:smoke .
docker build --target opm-orchestrator -t opm-api:smoke .  # use command override in compose
docker build --target opm-runner-task -t opm-runner-task:smoke .
```

Production and NAS hosts must use `*:nas` tags only — never deploy smoke tags there.

## Compose

See `OPA-Stack` profile `compose.opm.yaml` when co-deployed with the family stack.

The **OPM-Dashboard** web dashboard is published at **http://127.0.0.1:8098**
(`8098:80` on `opm-dashboard`). Open that URL in a browser after the stack is up.
