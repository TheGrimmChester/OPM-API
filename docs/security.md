# Security

## User auth

When `OPA_AUTH_REQUIRED` is enabled, control-plane routes require a user JWT (`Authorization: Bearer` or cookie) via **Open-Auth-Go** middleware. Shared `JWT_SECRET` with OPA hub is typical when co-deployed. OPM does not issue local login tokens.

## Service auth

Peer calls use `OPEN_SERVICE_JWT_SECRET` (prefer distinct from user `JWT_SECRET`). Claims: `iss`, `aud`, `sub=service`, `scope`, short `exp`, optional `org_id`.

## Tenancy

Optional `X-Organization-ID` / `X-Project-ID` headers for isolation when co-deployed with the family.

## Job sandbox

Task-automation runners must not receive `JWT_SECRET`, service JWTs, or connector secrets. Use `Open-Job-Go` scrubbing and hardened `docker run` (cap-drop, read-only, no docker.sock in jobs).

## Secrets on disk

OPM does not store GitHub App private keys or PATs. Those live in ORA connectors. Board/task JSON under `OPM_DATA_DIR` must not contain clone tokens. Ephemeral job credentials are request-scoped and discarded with the tmp workspace.
