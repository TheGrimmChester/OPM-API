# Interop

OPM is an optional peer. Empty `PEER_*_URL` values turn off cross-product features.

## Peer environment

```bash
PEER_OPA_URL=
PEER_ORA_URL=
PEER_OSA_URL=
PEER_OPL_URL=
PEER_OPM_URL=          # set on other products that call OPM
OPM_PUBLIC_URL=
OPEN_SERVICE_JWT_SECRET=
JWT_SECRET=
```

## Allowed peer calls (when configured)

| Caller → Callee | Purpose | Required? |
|-----------------|---------|-----------|
| OPM → ORA | Deep-link / delegate review jobs | Optional |
| OPM → OSA | Optional AppSec context for a task | Optional |
| OPM → OPA hub | Optional observability deep-links | Optional |
| ORA → OPM | Roadmap / task handoff from review plane | Optional |
| Dashboard → foreign API | **Forbidden** — UI talks only to `opm-api` | — |

Service JWTs use `iss` / `aud` / `scope` / short `exp`. Probe path: `GET /api/peer/health` with scope `health:read`.

## Boundary

Code-review and Repo Watch stay in ORA. OPM owns project/kanban/roadmap/task automation. Prefer deep-link or delegate over duplicating review forever.
