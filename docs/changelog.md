# Changelog

## Unreleased

- Feature: Roadmap/ideation agent generators — `run-roadmap-discovery`, `run-roadmap-features`, and `run-ideation` write vision/phases/features/ideas (builtin helpers after optional container spawn); optional `ideationType` on enqueue.
- Feature: Skip-to-phase — `skip-to-phase` job with `targetPhase` marks earlier plan subtasks complete and advances progress; exposed on task actions.

- Feature: two-way GitHub Issue sync for tasks (`…/github/issues/{link,unlink,push,pull}` via ORA peer `scm:pm`). Push sends task title/description and the board column's issue state; pull mirrors issue state, assignee, labels and milestone, moving the task on the `done`/reopen boundary only. Title divergence is reported rather than resolved (`adoptTitle` opts in). Failures persist on the task as `githubIssueSyncError` and in the `github-issue-sync` spec log with a machine-readable `status`.

- Feature: GitHub Milestones + Projects v2 bind/sync via ORA peer `scm:pm` (`…/github/milestones`, `…/github/projects`, task/roadmap fields, Status sync on board move).

- Feature: Model-backed planning/implementation/review inside `opm-runner-task` (OpenAI-compatible `OPM_MODEL_*` env from compose; honest fallback + builtin artifacts when key missing).


- Feature: Containerized task spawn — docker CLI in `opm-api` image; jobs `docker run` `opm-runner-task` when spawnReady; builtin fallback via `OPM_FORCE_BUILTIN` or spawn failure; `/api/spawn-probe` returns `spawnReady: true` when docker + image work.
- Auth: adopt Open-Auth-Go per-user project ACLs (`project_ids` / `EnforceProjectACL` on Gate middleware). Restricted JWTs get **403** on non-member `X-Project-ID`; role `admin` stays unrestricted. No second membership store — hub-minted claims only.
- Builtin job runner writes `spec.md`, plan, progress, review/QA artifacts, changelog; `POST …/approve`, `GET …/spec|logs|actions`; `PUT …/changelog`.
- Default runner tag `nas`.
- Bump `open-tenant-go` to v0.2.2 so auth-enforced list scope matches `WriteTenant` defaults.
- Docs: tenant header defaults under auth; NAS curl examples in interop/security/api.
- Auth via Open-Auth-Go `Gate` (delete local `auth.go` duplicate; validate-only, no local login routes).

## 0.2.0

- Projects are GitHub repositories only (`owner/repo` + ORA `connectorId`).
- Board/task state moves under `$OPM_DATA_DIR/projects/<id>/` (no durable local clone registry).
- Hub linkage (`PEER_OPA_URL`): org discovery; ORA linkage (`PEER_ORA_URL`): connectors, repos, tmp-clone credentials.
- Job workspaces: ephemeral clone under `OPM_JOB_TMP`, cleaned up after the run.
- Removed local-folder / `path` project create.

## 0.1.1

- Document that the companion UI is the OPM-Dashboard web dashboard (browser:
  http://127.0.0.1:8098 on smoke compose).

## 0.1.0

- Initial Open Project Manager API: multi-project registry, kanban, tasks/specs/plans, roadmap, ideation, changelog, job stub with `Open-Job-Go` runner wiring.
- Images: `opm-api`, `opm-orchestrator` command, `opm-runner-task`.
