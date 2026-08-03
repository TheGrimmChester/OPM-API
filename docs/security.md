# Security

## User auth

When `OPA_AUTH_REQUIRED` is enabled, control-plane routes require a user JWT (`Authorization: Bearer` or cookie). Shared `JWT_SECRET` with OPA hub is typical when co-deployed.

## Service auth

Peer calls use `OPEN_SERVICE_JWT_SECRET` (prefer distinct from user `JWT_SECRET`). Claims: `iss`, `aud`, `sub=service`, `scope`, short `exp`, optional `org_id`.

## Tenancy

Optional `X-Organization-ID` / `X-Project-ID` headers for isolation when co-deployed with the family.

## Job sandbox

Task-automation runners must not receive `JWT_SECRET`, service JWTs, or connector secrets. Use `Open-Job-Go` scrubbing and hardened `docker run` (cap-drop, read-only, no docker.sock in jobs).

## Secrets on disk

Do not store provider credentials in `.opm/` JSON. Keep model-provider credentials in the environment or a secrets manager.
