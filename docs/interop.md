# Interop

OPM is strongly linked to **OPA-Hub** for identity/tenancy, **OAM** for connector/credential storage and per-job model bindings, and **ORA** for GitHub SCM protocol (list connectors, clone, PR, issues/PM). Empty `PEER_*_URL` values turn off those features.

## Peer environment

```bash
PEER_OPA_URL=http://hub:8080          # required for org discovery in co-deployed stacks
PEER_OAM_URL=http://oam-api:8090      # account plane: model/key resolve + agent catalog
PEER_ORA_URL=http://ora-api:8091      # required for GitHub SCM protocol / tmp clones / delivery
PEER_OSA_URL=                         # reserved; unused by OPM today
PEER_OPL_URL=                         # reserved; unused by OPM today
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
| Organization directory | **OPA-Hub** (`GET /api/tenancy/organizations`; reads OAM when `PEER_OAM_URL` is set on hub) |
| Connector / AI credential **storage** + model bindings | **OAM** |
| GitHub App / PAT **protocol** (install, callback, clone/push/PR, issues) | **ORA** (uses OAM-stored credentials) |
| Kanban / roadmap / task jobs / delivery apply | **OPM** |
| Code review / Repo Watch | **ORA** (deep-link; do not duplicate) |

With `PEER_OAM_URL` set, each job resolves its model **and** API key from OAM (`creds:resolve`) and fails closed when the org has no credential. `OPM_MODEL*` / `CURSOR_API_KEY` are the legacy path used only when OAM is unset.

## Allowed peer calls

| Caller → Callee | Purpose | Scopes |
|-----------------|---------|--------|
| OPM → Hub | Org list, GitHub status advertisement | `health:read` (peer probe) |
| OPM → OAM | Per-job model + API key | `creds:resolve` |
| OPM → OAM | Publish agent catalog on boot | `catalog:write` |
| OPM → ORA | List connectors / repos | `connectors:read` |
| OPM → ORA | Short-lived clone credentials for job tmp workspaces | `scm:clone` |
| OPM → ORA | Milestones + Projects v2 list/bind/sync; issues | `scm:pm` |
| OPM → ORA | Push credentials, open/merge pull requests | `scm:pr` |
| OPM → ORA | Deep-link / delegate review (optional; not implemented) | review scopes as needed |
| ORA → OPM | SCM checker fan-out | `scm:events` (`POST /api/peer/scm/events`) |
| Dashboard → foreign API | **Forbidden** — UI talks only to `opm-api` | — |

## SCM checker peer (stub)

```
POST /api/peer/scm/events   (service JWT, scope scm:events, aud=opm-api)
→ { "checkers": [] }
```

Future checker **`opm:delivery`**: PR merge / issue events → task state sync (uses existing ORA peer SCM APIs). Family contract: [OPA-Stack interop — SCM checker platform](https://github.com/TheGrimmChester/OPA-Stack/blob/main/docs/interop.md#scm-checker-platform).

Service JWTs use `iss=opm-api` / `aud=ora-api|opa-hub|oam-api` / short `exp`. Probe: `GET /api/peer/health`.

## Boundary

Code-review and Repo Watch stay in ORA. OPM owns project/kanban/roadmap/task automation and applies recorded change sets (commit/push/PR via ORA). Prefer deep-link or delegate over duplicating review.

## Tenant headers

When `OPA_AUTH_REQUIRED=1`, send **`X-Organization-ID`** and **`X-Project-ID`** with the hub JWT on control-plane calls (and when proxying ORA connectors). Sibling ClickHouse products (OSA / OPL) scope to **`default-org` / `default-project`** when headers are omitted (Open-Tenant-Go ≥ 0.2.2); OPM project registry filtering also keys off organization — always set them so scripts match the dashboard picker.

```bash
TOKEN=$(curl -sf -X POST http://127.0.0.1:18080/api/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"admin"}' | jq -r .token)

curl -sf http://127.0.0.1:8096/api/projects \
  -H "Authorization: Bearer $TOKEN" \
  -H "X-Organization-ID: default-org" \
  -H "X-Project-ID: default-project" | jq '.projects | length'

curl -sf "http://127.0.0.1:8096/api/github/connectors?organizationId=default-org" \
  -H "Authorization: Bearer $TOKEN" \
  -H "X-Organization-ID: default-org" \
  -H "X-Project-ID: default-project" | jq '.connectors | length'
```

From the LAN use `192.168.100.101` instead of `127.0.0.1`. See [OPA-Stack interop](https://github.com/TheGrimmChester/OPA-Stack/blob/main/docs/interop.md#tenant-headers-required-when-auth-is-on).
