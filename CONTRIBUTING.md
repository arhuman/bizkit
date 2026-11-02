# Contributing

How to build, test, and submit changes. Keep it short and true; delete sections that do not apply.

## Setup

```bash
# clone, then bring up dependencies (DB, etc.)
TODO
```

## Make targets

| Target | Purpose |
| ------ | ------- |
| `make test` | Short unit tests (`-short`) for the red/green loop. |
| `make fulltest` | Every package with the race detector and coverage. Run before committing. |
| `make test-s3` | S3 backend suite against a disposable MinIO from `docker-compose.test.yml` (needs Docker). |
| `make cover` | Tests with coverage; fails below `COVER_MIN`. |
| `make audit` | `go mod verify` + `gofmt` + file-length check + `golangci-lint` + `govulncheck` + coverage gate. Same command locally and in CI. |
| `make tidy` | `go fmt` + `go mod tidy`. |
| `make ci` | Full local pipeline (`tidy` + `audit` + `fulltest`). |
| `make release` | Derive the next semver from Conventional Commits, gate via `make ci`, tag and push. |

## Commit messages

[Conventional Commits](https://www.conventionalcommits.org/): `type(scope): subject`, type one of
`feat|fix|docs|style|refactor|perf|test|build|ci|chore|revert`. Enforced locally (commit-msg hook)
and in CI (commitlint on PRs).

## Before opening a PR

1. `make ci` passes.
2. Update `CHANGELOG.md` under `[Unreleased]`.
3. TODO: project-specific checks (e.g. verify UI flows in a browser).
