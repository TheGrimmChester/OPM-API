# Operations

## Health

- API: `GET /api/health` on `LISTEN_ADDR` (default `:8096`)
- Orchestrator: `GET /api/health` on `ORCHESTRATOR_LISTEN_ADDR` (default `:8099`)

## Logs

Structured stderr from the Go process. Job snapshots live under `<project>/.opm/runs/`.

## Data

Back up `OPM_DATA_DIR/projects.json` and each project's `.opm/` directory. Deleting a registry entry does not remove workspace files.

## Upgrades

Replace the `opm-api` / `opm-orchestrator` image. Prefer rolling the API before
the web dashboard image (`opm-dashboard`). Use `*:nas` on production hosts.
Smoke UI: http://127.0.0.1:8098.

## Reaper

Orchestrator owns labeled containers (`opm.job=<id>`, `opm.instance=<id>`). Reap orphaned runners after job terminal states.
