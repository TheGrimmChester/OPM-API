# Changelog

## Unreleased

- AutoCursor parity: builtin job runner writes `spec.md`, plan, progress, review/QA artifacts, changelog; `POST …/approve`, `GET …/spec|logs|actions`; `PUT …/changelog`.
- Jobs: honest stub execution — `execution: "stub"` + operator `message` (workspace + docker argv only; container not run). Default runner tag `nas`.
- Docs: remaining backlog (plan UI, DnD, real runner spawn).
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
