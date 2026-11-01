# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- `davfs`: WebDAV `http.Handler` over a host `Tree`, confining paths and MOVE/COPY destinations to the mount, committing a PUT only after a complete body, and answering 403 and 413 from the shared sentinel errors.

## [0.1.0] - 2026-11-01

### Added

- `blob`: storage `Backend` contract with `ErrNotFound` and `ErrTooLarge`, a local filesystem backend and the `blobtest` conformance suite.
- `blob/s3`: S3 backend over minio-go with prefix isolation, verified against MinIO by `make test-s3`.
- `fileserve`: `Serve` with an inline type allowlist, Range support, `nosniff` and a sandbox CSP.
- `fileserve`: streaming multipart reader with per-file, count, total and field limits, plus an active-content sniff.
- `sharelink`: share tokens, PIN hashing with failure lockout, an expiry policy, per-link upload caps and an AES-GCM `Sealer`.
- `calendar`: timezone-aware `MonthGrid` and upcoming-events `Agenda`.
- `calendar/ics`: iCalendar writer and `FeedHandler` with ETag, 304 support and token redaction for logs.
- `ginx`: gin adapters that expose gin keys to `net/http` handlers through the request context.
- Sentinel errors shared across packages, matched with `errors.Is`.
- `make release` cuts a release, with the version derived from Conventional Commits and gated on `make ci`.

### Changed

- Require Go 1.26.10.

### Fixed

- `make test-s3` tears MinIO down even when its output pipe closes early.
- Bump `golang.org/x/net` to v0.62.0 for GO-2026-6866 and GO-2026-6860.
