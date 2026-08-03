# Interop

OPM is strongly linked to **OPA-Hub** for identity/tenancy and to **ORA** for GitHub credentials. Empty `PEER_*_URL` values turn off those features.

## Peer environment

```bash
PEER_OPA_URL=http://hub:8080          # required for org discovery in co-deployed stacks
PEER_ORA_URL=http://ora-api:8091      # required for GitHub connectors / tmp clones
PEER_OSA_URL=
PEER_OPL_URL=
PEER_OPM_URL=                         # set on other products that call OPM
OPM_PUBLIC_URL=
OPEN_SERVICE_JWT_SECRET=
JWT_SECRET=                           # shared with hub when AUTH_MODE=codeployed
AUTH_MODE=codeployed
```

## Credential model (one source of truth)

| Concern | Product |
|---------|---------|
| User login / JWT issuer | **OPA-Hub** |
| Organization directory | **OPA-Hub** (`GET /api/tenancy/organizations`) |
| GitHub App / PAT connectors | **ORA** only |
| Kanban / roadmap / task jobs | **OPM** |
| Code review / Repo Watch | **ORA** (deep-link; do not duplicate) |

## Allowed peer calls

| Caller → Callee | Purpose | Scopes |
|-----------------|---------|--------|
| OPM → Hub | Org list, GitHub status advertisement | `health:read` (peer probe) |
| OPM → ORA | List connectors / repos | `connectors:read` |
| OPM → ORA | Short-lived clone credentials for job tmp workspaces | `scm:clone` |
| OPM → ORA | Deep-link / delegate review (optional) | review scopes as needed |
| Dashboard → foreign API | **Forbidden** — UI talks only to `opm-api` | — |

Service JWTs use `iss=opm-api` / `aud=ora-api|opa-hub` / short `exp`. Probe: `GET /api/peer/health`.

## Boundary

Code-review and Repo Watch stay in ORA. OPM owns project/kanban/roadmap/task automation. Prefer deep-link or delegate over duplicating review.
