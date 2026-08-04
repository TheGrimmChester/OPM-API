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

## 5. Bind GitHub Milestones and Projects

After linking a repo:

1. **Roadmap** or **Board → task detail** → pick a **GitHub Milestone** (listed from the connected repo via ORA).
2. **Bind GitHub Project** (Projects v2) once per OPM project; then **Sync to Project** on tasks/features creates a draft item and best-effort maps board Status.

## 6. Link a task to a GitHub Issue

A task can be backed by an Issue in the project's linked repo. From **Board → task detail**, either attach an existing issue by number or create one from the task; then push local edits to it and pull remote changes back.

```bash
BASE=http://<host>:8096/api/projects/$PROJECT_ID/github/issues
H=(-H "Authorization: Bearer $TOKEN" -H "X-Organization-ID: default-org" -H "X-Project-ID: $PROJECT_ID" -H 'Content-Type: application/json')

# Create an issue from the task (title/body default to the task's)
curl -sf "${H[@]}" -X POST $BASE/link  -d '{"specId":"'$SPEC'","create":true}'
# …or attach an existing one (verified to exist before the link is stored)
curl -sf "${H[@]}" -X POST $BASE/link  -d '{"specId":"'$SPEC'","issueNumber":42}'

curl -sf "${H[@]}" -X POST $BASE/push  -d '{"specId":"'$SPEC'"}'
curl -sf "${H[@]}" -X POST $BASE/pull  -d '{"specId":"'$SPEC'"}'
curl -sf "${H[@]}" -X POST $BASE/unlink -d '{"specId":"'$SPEC'"}'
```

### Field mapping

Authority is split per field, not per record. OPM owns what the board owns; GitHub owns the rest.

| Field | Direction | Notes |
|-------|-----------|-------|
| Task title → issue title | push | OPM is authoritative |
| Task description → issue body | push | OPM is authoritative |
| Board column → issue state | push | Explicit table below |
| Task milestone → issue milestone | push | Pushed when the task has one; a task with no milestone does **not** clear the issue's |
| Issue state → board column | pull | See the reverse table below |
| Issue assignee → `githubIssueAssignee` | pull | Mirror only; OPM has no assignee model, so it is never pushed |
| Issue labels → `githubIssueLabels` | pull | Mirror only; OPM does not model task labels, so labels are never pushed |
| Issue milestone → task milestone | pull | GitHub wins on refresh |
| Issue title → `githubIssueTitle` | pull | Mirrored **without** overwriting the task title — see conflicts |

**Board column → issue state (push).** Total: every column maps to exactly one state.

| Column | Issue state |
|--------|-------------|
| `backlog`, `queue`, `in_progress`, `review`, `human_review` | `open` |
| `done` | `closed` |

**Issue state → board column (pull).** Deliberately lossy and conservative, because five columns map to `open`:

| Issue state | Task column | Action |
|-------------|-------------|--------|
| `closed` | any except `done` | move to `done` |
| `open` | `done` | move to `in_progress` (the issue was reopened) |
| `open` | anything else | **no move** — `open` carries no column information |

That last row is why a refresh cannot drag a task out of `human_review`.

### Conflicts and failures

- **Title divergence.** If the issue title differs from the task title, `pull` keeps the task title, returns `titleDiverged: true`, and reports it. Send `{"adoptTitle": true}` to take the issue title instead. There is no silent last-writer-wins.
- **Failures are recorded, never swallowed.** Any push/pull failure is persisted on the task as `githubIssueSyncError`, appended to the spec log `github-issue-sync`, and returned with a machine-readable `status`. The field is cleared on the next success, so a stale error never lingers and a broken link never looks healthy.

| `status` | HTTP | Meaning |
|----------|------|---------|
| `missing_issues_permission` | 403 | Connector lacks Issues write; response carries `missing` |
| `issue_not_found` | 404 | Issue deleted, transferred, or invisible to the connector. The link is **kept** so it can be repaired |
| `no_issue_linked` | 400 | Push/pull on an unlinked task |
| `not_linked_repo` | 400 | OPM project has no repo or connector |
| `upstream_error` | 502 | Anything else; carries GitHub's status and body |

`unlink` only drops the link — the GitHub issue is left open and untouched.

**GitHub App permissions on the ORA connector:**

| Feature | Required permission |
|---------|---------------------|
| Milestones | **Issues** read/write |
| Issue link / push / pull | **Issues** read/write |
| Projects v2 | **Organization projects** write (optional; without it milestone bind and Issue sync still work) |

Issue sync needs no permission beyond the Issues read/write that milestones already require, so an install that can bind milestones can sync issues. PAT connectors need equivalent classic or fine-grained scopes (`repo` / Issues + Projects).

## Troubleshooting

| Symptom | Likely cause | Fix |
|---------|--------------|-----|
| Login returns 503 on `/api/auth/login` | Co-deployed mode | Use hub login (`/hub-auth`); expected behaviour |
| `PEER_ORA_URL not configured` on connectors | Missing peer env | Set `PEER_ORA_URL` on `opm-api`, recreate container |
| Empty connector list, hub OK | No GitHub App install / wrong org | Install app in ORA; check `organizationId` |
| `ora unavailable` | ORA down or service JWT mismatch | Check `ora-api` health; verify `OPEN_SERVICE_JWT_SECRET` |
| `missing scope` on milestones/projects | ORA image without `scm:pm` peer routes, or JWT missing scope | Redeploy `ora-api:nas` + `opm-api:nas` together |
| `missing_organization_projects` | App lacks Projects permission | Grant **Organization projects** on the GitHub App install |
| `missing_issues_permission` on issue link/push/pull | App lacks Issues write | Grant **Issues** read/write and re-accept the install; response lists `missing` |
| `issue_not_found` on pull | Issue deleted or transferred | Unlink and attach the new number; the link is kept until you do |
| Task shows a sync error badge | Last push/pull failed | Read `githubIssueSyncError` on the task or the `github-issue-sync` spec log |
| Job fails: `git: executable file not found` | `opm-api` image missing git | Rebuild `opm-api:nas` with git in runtime stage |
| Hub org list shows only `default-org` | `PEER_OPA_URL` unset | Point OPM at hub; confirm tenancy API |

## Security notes

- Dashboard talks **only** to `opm-api`; never call ORA or Hub directly from the browser.
- Clone tokens for task jobs are fetched from ORA per request and discarded with ephemeral workspaces.
- Milestone/Project/Issue sync uses short-lived service JWTs (`scm:pm`); GitHub secrets never enter OPM storage.
- See [security.md](security.md) and [interop.md](interop.md) for service JWT and tenancy boundaries.
