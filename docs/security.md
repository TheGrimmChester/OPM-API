# Security

## User auth

When `OPA_AUTH_REQUIRED` is enabled, control-plane routes require a user JWT (`Authorization: Bearer` or cookie) via **Open-Auth-Go** middleware. Shared `JWT_SECRET` with OPA hub is typical when co-deployed. OPM does not issue local login tokens.

## Service auth

Peer calls use `OPEN_SERVICE_JWT_SECRET` (prefer distinct from user `JWT_SECRET`). Claims: `iss`, `aud`, `sub=service`, `scope`, short `exp`, optional `org_id`.

## Tenancy

When `OPA_AUTH_REQUIRED=1`, send **`X-Organization-ID`** / **`X-Project-ID`** on control-plane requests. Sibling ClickHouse list APIs (OSA / OPL) scope to `default-org` / `default-project` when headers are omitted (Open-Tenant-Go ≥ 0.2.2); OPM should mirror the same tenant picker headers for consistent isolation. Hub JWTs with `project_ids` are allowlisted via Open-Auth-Go `EnforceProjectACL` (non-member → **403**; role `admin` unrestricted).

## Job sandbox

Task-automation runners must not receive `JWT_SECRET`, service JWTs, or connector secrets. Use `Open-Job-Go` scrubbing and hardened `docker run` (cap-drop, read-only, no docker.sock in jobs).

## Secrets on disk

OPM does not store GitHub App private keys or PATs. Those live in **OAM** (account plane); ORA issues short-lived SCM credentials from that store. Board/task JSON under `OPM_DATA_DIR` must not contain clone tokens. Ephemeral job credentials are request-scoped and discarded with the tmp workspace.

## Code delivery credentials

A delivery pushes a branch and opens a pull request, so it needs write authority
that a read-only clone credential does not carry. Protocol stays in **ORA**
(credentials are stored in **OAM**):

- OPM requests a short-lived **Contents-write** credential from ORA
  (`POST /api/peer/scm/push-credentials`, service JWT scope **`scm:pr`**) for
  exactly one push, and asks ORA to open the pull request
  (`POST /api/peer/scm/pull-requests/create`, same scope). `scm:pr` is the only
  scope that can write code; `scm:pm` (issues, milestones, Projects) cannot.
- The credential reaches git only through `GIT_ASKPASS` plus an environment
  variable. It is never placed in argv (visible in `ps`), never embedded in a
  remote URL (which git records in `.git/config` and echoes in errors), never
  written under `OPM_DATA_DIR`, and never persisted on a task.
- Git output that can reach an API caller is passed through a redactor that
  replaces the credential with `[redacted]`, as a second line of defence.
- The askpass helper lives inside the ephemeral workspace and is removed with it.

## What a delivery may write

Paths are validated against the workspace before anything is written, and the
whole change set is refused if any entry fails:

- absolute paths and any `..` segment are rejected
- `.git/` is rejected — a delivery changes source, not repository metadata
- `.github/workflows/` is rejected: the delivery credential deliberately has no
  `workflows` permission, so GitHub would refuse the push anyway
- `OPM_DELIVERY_MAX_FILES` / `OPM_DELIVERY_MAX_BYTES` bound the change set

Staging is explicit (`git add -- <applied paths>`), never `git add -A`, so only
files the apply step actually wrote can enter a commit. A change set identical to
the branch tip produces `no_changes_produced` rather than an empty commit.
