# Repository guidance

- Keep code, comments, docs, configuration, and commits in English.
- Preserve bounded scheduler queues and explicit concurrency limits.
- Never log configured HTTP header values or a PostgreSQL DSN.
- Keep database state transitions serialized per endpoint.
- Maintenance results must not consume SLO error budget.
- Tests must use local fake servers and synthetic data.
- Run gofmt, vet, race-enabled tests, builds, and the controlled Compose demo before release.
- Do not commit `.env`, production endpoints, credentials, certificates, or private keys.

