# Contributing

How to build, test, and submit changes. Keep it short and true; delete sections that do not apply.

## Setup

- Go 1.26.10 or newer (the `go` directive in `go.mod`; CI also tests the latest stable release).
- `golangci-lint` and `govulncheck` at the versions pinned in the `Makefile`: `make tools` installs
  them, and `make audit` runs it when they are missing or at another version.
- Docker with Compose v2, for `make test-s3` only. No other service is needed: the library owns no
  database, and every other test runs in-process.

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
3. If the change touches `blob/s3`, `make test-s3` passes (CI runs it on every PR).
