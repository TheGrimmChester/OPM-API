# API

Base path: `/api`. JSON request/response. Auth optional unless `OPA_AUTH_REQUIRED` is set.

## Health

- `GET /api/health` → `{ status, service: "opm-api", version }`
- `GET /api/peer/health` → service JWT probe (`health:read` when `OPEN_SERVICE_JWT_SECRET` set)

## Projects

- `GET /api/projects` → `{ projects: Project[] }`
- `POST /api/projects` `{ name, path }` → `Project` (initializes `.opm/`)
- `GET /api/projects/{id}` → `Project`
- `DELETE /api/projects/{id}` → 204 (registry only; does not delete workspace files)
- `POST /api/projects/{id}/init` → ensure `.opm/` layout

## Board and tasks

- `GET /api/projects/{id}/board` → `{ columns, board }`
- `PUT /api/projects/{id}/board` → replace column membership
- `GET /api/projects/{id}/tasks` → `{ tasks }`
- `POST /api/projects/{id}/tasks` `{ title?, description, requireReviewBeforeCoding?, ideationId? }`
- `GET /api/projects/{id}/tasks/{specId}`
- `PATCH /api/projects/{id}/tasks/{specId}`
- `DELETE /api/projects/{id}/tasks/{specId}`
- `POST /api/projects/{id}/tasks/{specId}/move` `{ toStatus }`
- `GET /api/projects/{id}/tasks/{specId}/plan`
- `GET /api/projects/{id}/tasks/{specId}/progress`

Columns: `backlog`, `queue`, `in_progress`, `review`, `human_review`, `done`.

## Roadmap / ideation / changelog

- `GET|PUT /api/projects/{id}/roadmap`
- `GET|PUT /api/projects/{id}/ideation`
- `GET /api/projects/{id}/changelog` → `{ markdown }`

## Status and jobs

- `GET /api/projects/{id}/status`
- `GET /api/projects/{id}/jobs`
- `POST /api/projects/{id}/jobs` `{ action, specId? }`
- `GET /api/projects/{id}/jobs/{runId}`
- `POST /api/projects/{id}/jobs/{runId}/cancel`

Job actions include: `run-planning`, `run-implementation`, `run-review`, `run-qa-fix`, `run-roadmap-discovery`, `run-roadmap-features`, `run-ideation`, `generate-changelog`.

The current orchestrator stubs job completion without always spawning a container; hardened `docker run` argv is prepared via `Open-Job-Go` for the runner path.
