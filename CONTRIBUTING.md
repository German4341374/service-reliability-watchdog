# Contributing

Use a focused branch and Conventional Commits such as `feat(checker): add a protocol probe` or
`fix(store): serialize endpoint transitions`. Do not include production endpoints, credentials,
private certificates, customer data, or copied incident records.

Before opening a pull request:

```bash
make lint
make test
make build
docker compose config --quiet
```

Changes to scheduling, retries, state calculation, SLOs, persistence, or alerts require tests.
Changes to failure behavior require a corresponding runbook update.

