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
2. **Bind GitHub Project** (Projects v2) once per OPM project; then **Sync to Project** on tasks/features creates a draft item and best-effort maps board Status. **Unbind** reverses either level — the project-level bind or a single task's board item — and leaves the GitHub Project and its items untouched.
3. Renaming a task or editing its description re-syncs the bound board item. If the board cannot be updated the reason is shown on the task (`githubProjectSyncError`) and written to the `github-project-sync` spec log; it is never reported as success. Note this needs the organization projects permission below.

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
| Clone a repo for a job | **Contents** read, **Metadata** read |
| Milestones | **Issues** read/write |
| Issue link / push / pull | **Issues** read/write |
| Projects v2 — list, item sync, title refresh, Status on move | **Organization permissions › Projects: Read and write** (`organization_projects: write`); without it milestone bind and Issue sync still work |
| Push a delivery branch | **Contents** read **and write**, **Metadata** read |
| Open a delivery pull request | **Pull requests** read **and write** (plus Contents write, Metadata read) |

Issue sync needs no permission beyond the Issues read/write that milestones already require, so an install that can bind milestones can sync issues.

**Workflows is never requested.** A delivery therefore cannot modify
`.github/workflows/`, and OPM refuses such paths before committing rather than
letting the push fail.

PAT connectors need equivalent classic or fine-grained scopes (`repo` / Issues +
Projects; Contents and Pull requests read/write for delivery). ORA refuses PAT
writes unless `OPA_AGENTS_ALLOW_PAT_WRITE=1`, so a GitHub App connector is the
supported path for delivery.

## 6. Deliver a task as a pull request

Once an implementation run has recorded file changes (which requires a model
runner — see `OPM_MODEL_API_KEY` in [configuration.md](configuration.md)), the
task detail **Delivery** section lists them and offers **Deliver as pull
request**. That commits them on `opm/<specId>`, pushes the branch, and opens a
pull request; the board then shows a **PR #n** badge on the card.

With no model runner configured, the builtin path advances plan state and writes
no source code, so Delivery reports that there is nothing to deliver instead of
implying code was shipped.

**The current App installation does not grant `organization_projects`.** Everything Projects v2 therefore
returns `missing_organization_projects` — with the permission named, rather than failing quietly — while
milestone bind and Issue sync keep working. To enable it: edit the GitHub App's **Organization permissions**,
set **Projects** to **Read and write**, then re-accept the installation's updated permissions in the
organization's settings. The live board update can only be proved after that is deployed.

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
| `no_changes_produced` on deliver | No run recorded file changes (builtin path, or the model returned none) | Set `OPM_MODEL_API_KEY` and re-run implementation; check the task's `delivery` log |
| `missing_contents_permission` on deliver | App cannot push | Grant **Contents: Read and write**, re-accept the installation permissions |
| `missing_pull_requests_permission` on deliver | App cannot open pull requests | Grant **Pull requests: Read and write**, re-accept the installation permissions |
| `push_rejected` on deliver | The delivery branch already exists on the remote with different commits | Re-deliver with a different `branch` in the request body |
| `unsafe_path` on deliver | A change targeted `..`, an absolute path, `.git/` or `.github/workflows/` | Expected — refused by design; fix the run that produced it |
| `missing scope` on deliver | ORA image without the `scm:pr` peer routes | Redeploy `ora-api:nas` + `opm-api:nas` together |
| Job fails: `git: executable file not found` | `opm-api` image missing git | Rebuild `opm-api:nas` with git in runtime stage |
| Hub org list shows only `default-org` | `PEER_OPA_URL` unset | Point OPM at hub; confirm tenancy API |

## Security notes

- Dashboard talks **only** to `opm-api`; never call ORA or Hub directly from the browser.
- Clone tokens for task jobs are fetched from ORA per request and discarded with ephemeral workspaces.
- Milestone/Project/Issue sync uses short-lived service JWTs (`scm:pm`); GitHub secrets never enter OPM storage.
- Code delivery uses the separate, narrower `scm:pr` scope for the push credential and the pull-request call. The credential is per-request, reaches git only through `GIT_ASKPASS`, and is never written to disk or logs.
- See [security.md](security.md) and [interop.md](interop.md) for service JWT and tenancy boundaries.
