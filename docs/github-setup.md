# GitHub connector setup (hub + ORA)

OPM links **GitHub repositories** discovered through **OPA-Hub** (identity/orgs) and **ORA** (connectors). OPM never stores GitHub App keys or PATs.

## Prerequisites

| Component | Requirement |
|-----------|-------------|
| **OPA-Hub** | Running; user login issues JWTs (`AUTH_MODE=codeployed` on OPM) |
| **ORA-API** | GitHub App or PAT connectors configured |
| **OPM-API** | `PEER_OPA_URL` and `PEER_ORA_URL` set in compose |
| **OPM-Dashboard** | Browser UI on `:8098`; login via `/hub-auth` in co-deployed mode |

Verify peers from an authenticated session:

```bash
curl -H "Authorization: Bearer $TOKEN" http://<host>:8098/api/hub/status
curl -H "Authorization: Bearer $TOKEN" http://<host>:8098/api/hub/organizations
curl -H "Authorization: Bearer $TOKEN" "http://<host>:8098/api/github/connectors?organizationId=default-org"
```

## 1. Configure GitHub App on ORA

On `ora-api`, set (see [configuration.md](configuration.md)):

- `OPA_GITHUB_APP_ID`
- `OPA_GITHUB_APP_SLUG`
- `OPA_GITHUB_APP_PRIVATE_KEY`
- `OPA_GITHUB_WEBHOOK_SECRET`
- `OPA_CONNECTOR_SECRET` (for encrypted PAT storage)
- `OPA_PUBLIC_URL` / `ORA_PUBLIC_URL` (OAuth callback and webhooks)

Install the GitHub App on the org or account whose repos OPM should link.

## 2. Wire OPM peers

In the family stack compose (e.g. `OPA-Stack/compose.opm.yaml`):

```yaml
opm-api:
  environment:
    PEER_OPA_URL: http://hub:8080
    PEER_ORA_URL: http://ora-api:8091
    AUTH_MODE: codeployed
    JWT_SECRET: ${JWT_SECRET}   # shared with hub
    OPA_AUTH_REQUIRED: "true"
    OPM_RUNNER_TAG: nas         # production / NAS only
```

Rebuild with production tags (`opm-api:nas`, `opm-dashboard:nas`) — never smoke tags on NAS.

## 3. Sign in to OPM Dashboard

1. Open `http://<host>:8098/login`
2. In co-deployed mode, login is issued by **OPA-Hub** via same-origin `/hub-auth/api/auth/login` (not `opm-api` `/api/auth/login`, which returns 503 by design).
3. Confirm session: `GET /api/auth/status` → `"mode":"codeployed"`, `"authenticated":true`.

## 4. Link a repository

1. **Projects** → **Link GitHub repo**
2. Select **Hub organization** (from `GET /api/hub/organizations`)
3. Select **GitHub connector** (from ORA via `GET /api/github/connectors`)
4. Pick a **repository** and submit → `POST /api/projects`

## Troubleshooting

| Symptom | Likely cause | Fix |
|---------|--------------|-----|
| Login returns 503 on `/api/auth/login` | Co-deployed mode | Use hub login (`/hub-auth`); expected behaviour |
| `PEER_ORA_URL not configured` on connectors | Missing peer env | Set `PEER_ORA_URL` on `opm-api`, recreate container |
| Empty connector list, hub OK | No GitHub App install / wrong org | Install app in ORA; check `organizationId` |
| `ora unavailable` | ORA down or service JWT mismatch | Check `ora-api` health; verify `OPEN_SERVICE_JWT_SECRET` |
| Job fails: `git: executable file not found` | `opm-api` image missing git | Rebuild `opm-api:nas` with git in runtime stage |
| Hub org list shows only `default-org` | `PEER_OPA_URL` unset | Point OPM at hub; confirm tenancy API |

## Security notes

- Dashboard talks **only** to `opm-api`; never call ORA or Hub directly from the browser.
- Clone tokens for task jobs are fetched from ORA per request and discarded with ephemeral workspaces.
- See [security.md](security.md) and [interop.md](interop.md) for service JWT and tenancy boundaries.
