# Operations

## Backup

- `$OPM_DATA_DIR/projects.json` — linked GitHub projects registry
- `$OPM_DATA_DIR/projects/<id>/` — board, specs, roadmap, ideation, job records per linked repo

Ephemeral job clones under `OPM_JOB_TMP` are not durable and should not be backed up.

## Restore

Restore the data directory onto a host with the same `OPM_DATA_DIR`. Reconfigure `PEER_OPA_URL` / `PEER_ORA_URL` so discovery and clones work. Connectors themselves live in ORA (ClickHouse / connector store).

## Health

- `GET /api/health` on `opm-api`
- `GET /api/hub/status` — verify hub + ORA linkage
- `GET /api/peer/health` with a service JWT when `OPEN_SERVICE_JWT_SECRET` is set

## Migration from local-folder projects

Older registries used `path` (host directory) and `<path>/.opm/`. That model is removed. Re-link repositories via the dashboard (hub org → connector → GitHub repo). Do not copy legacy path entries forward.
