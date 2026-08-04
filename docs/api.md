# API

Base path: `/api`. JSON request/response. Auth optional unless `OPA_AUTH_REQUIRED` is set.

When auth is required (NAS), send `Authorization: Bearer <hub-jwt>` plus **`X-Organization-ID`** / **`X-Project-ID`**. See [interop.md](interop.md#tenant-headers) and [security.md](security.md).

## Health

- `GET /api/health` → `{ status, service: "opm-api", version }`
- `GET /api/peer/health` → service JWT probe (`health:read` when `OPEN_SERVICE_JWT_SECRET` set)

## Hub and GitHub discovery

- `GET /api/hub/status` → hub/ORA linkage summary (`credentials_home: ora`)
- `GET /api/hub/organizations` → organizations from OPA-Hub (falls back to `default-org`)
- `GET /api/github/connectors?organizationId=` → proxies ORA connector list
- `GET /api/github/connectors/{id}/repos?organizationId=` → proxies installable GitHub repos

Requires `PEER_OPA_URL` / `PEER_ORA_URL` as documented in [interop.md](interop.md).

## Projects (GitHub repos)

- `GET /api/projects` → `{ projects: Project[] }`
- `POST /api/projects` `{ connectorId, ownerRepo, organizationId?, name?, githubRepoId?, htmlUrl?, defaultBranch? }` → linked `Project`
- `GET /api/projects/{id}` → `Project`
- `DELETE /api/projects/{id}` → 204 (removes registry entry + server-side board data)
- `POST /api/projects/{id}/init` → ensure data-dir layout

`Project` fields: `id`, `name`, `ownerRepo`, `connectorId`, `organizationId`, `githubRepoId`, `htmlUrl`, `defaultBranch`, timestamps.

Local folder / `path` create payloads are rejected.

## Board and tasks

- `GET /api/projects/{id}/board` → `{ columns, board }`
- `PUT /api/projects/{id}/board` → replace column membership
- `GET /api/projects/{id}/tasks` → `{ tasks }`
- `POST /api/projects/{id}/tasks` `{ title?, description, requireReviewBeforeCoding?, ideationId? }`
- `GET /api/projects/{id}/tasks/{specId}`
- `PATCH /api/projects/{id}/tasks/{specId}`
- `DELETE /api/projects/{id}/tasks/{specId}`
- `POST /api/projects/{id}/tasks/{specId}/move` `{ toStatus }`
- `POST /api/projects/{id}/tasks/{specId}/approve` → move `human_review` → `queue` (approve for coding)
- `GET /api/projects/{id}/tasks/{specId}/plan`
- `GET /api/projects/{id}/tasks/{specId}/progress`
- `GET /api/projects/{id}/tasks/{specId}/spec` → `{ markdown }`
- `GET /api/projects/{id}/tasks/{specId}/logs` → `{ logs: { planning?, implementation?, validation? } }`
- `GET /api/projects/{id}/tasks/{specId}/actions` → valid run actions (`planning`, `implementation`, `review`, `qaFix`, `approve`, …)

Columns: `backlog`, `queue`, `in_progress`, `review`, `human_review`, `done`.

## Roadmap / ideation / changelog

- `GET|PUT /api/projects/{id}/roadmap`
- `GET|PUT /api/projects/{id}/ideation`
- `GET|PUT /api/projects/{id}/changelog` → `{ markdown }`

## Status and jobs

- `GET /api/projects/{id}/status`
- `GET /api/projects/{id}/jobs`
- `POST /api/projects/{id}/jobs` `{ action, specId? }`
- `GET /api/projects/{id}/jobs/{runId}`
- `POST /api/projects/{id}/jobs/{runId}/cancel`

Job actions include: `run-planning`, `run-implementation`, `run-review`, `run-qa-fix`, `run-followup-planning`, `run-roadmap-discovery`, `run-roadmap-features`, `run-ideation`, `generate-changelog`.

Jobs prepare an ephemeral clone (via ORA clone credentials when configured), then run the **builtin** executor (`execution: "builtin"`) which writes real `spec.md` / `implementation_plan.json` / `progress.json` / review artifacts / changelog. Containerized agent spawn (orchestrator + `opm-runner-task:nas`) remains follow-on — the runner image is still a placeholder.
