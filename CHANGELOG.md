# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.3.0] - 2026-11-10

### Added

- `blob/s3`: `Config.Transport` sets the `http.RoundTripper` every request to the store goes through, for a caller that must control dialing or proxying. Nil keeps the client's default transport.
- `blob`: `Backend.ListPage(ctx, prefix, after, limit)` returns at most `limit` objects with a key greater than `after`, in byte order, and the cursor for the next page (`""` on the last). `limit <= 0` means the new `DefaultPageSize` (1000). `blob/s3` lists from the cursor with `StartAfter`; `blob/local` keeps only about twice `limit` keys in memory while walking. `blobtest` gains a `ListPaged` case. Adding the method breaks any `blob.Backend` implemented outside bizkit (ADR-0001).
- `fileserve`: `SetPolicy(h, name, contentType)` sets the Content-Disposition, nosniff and sandbox Content-Security-Policy headers `Serve` sends, for a handler that writes the response itself.
- `fileserve`: `Limits.WithDefaults` returns the limits with every zero or negative numeric field set to its Default value, the same limits `NewReader` enforces. `sharelink.UploadCaps.Limits` now uses it.

### Changed

- `blob/local` and `blob/s3` normalize keys with one shared rule (backslashes to slashes, doubled slashes and dot segments collapsed, leading slash dropped), and `blobtest` gains a `KeyNormalization` case that checks the stored key.

### Fixed

- `blob/local`: `List` and `ListPage` with a prefix that does not end in `/` no longer descend into sibling directories that cannot hold a matching key. Listing `ws1` used to walk all of `ws2`, and an unreadable `ws2` failed the listing.
- `davfs`: a GET, HEAD or POST that serves a file now carries the `fileserve` policy: `X-Content-Type-Options: nosniff`, an inline Content-Disposition only for a type `fileserve.Inline` accepts, an attachment one otherwise, and the sandbox Content-Security-Policy for every type but PDF. Stored HTML or SVG used to be served as a live page on the origin mounting davfs.
- `davfs`: a MOVE or COPY Destination is now percent-decoded once, so the name `Options.Authorize` checks is the name the Tree receives. x/net/webdav used to decode it a second time: `/dav/public/%252e%252e/private/x` was authorized as a name under `/public` and then written to `/private/x`, and `100%2541.txt` became `100A.txt`.
- `blob`: the `Backend` contract now covers a key that is also a directory prefix of another key. A backend may store both or refuse the second `Put` with `ErrForbidden`, and `Delete` of a key that is only a prefix returns nil and removes nothing. `blobtest` checks both outcomes. `blob/local` refuses with `ErrForbidden`; it used to fail with raw rename, mkdir and "directory not empty" errors. `blob/s3` keeps storing both.
- `sharelink`: each PIN lock now lasts twice the previous one, from `PINLockDuration` up to the new `MaxPINLockDuration` (24 hours), and only a right PIN resets it. Hosts must persist the new `PINState.Lockouts` field. Every lock used to last 15 minutes, which allowed 480 guesses a day: every 4-digit PIN within a link's 30-day default lifetime.
- `davfs`: a PROPFIND no longer opens a file without an extension to sniff its type. `getcontenttype`, and the Content-Type of a GET, come from the name's extension, else `application/octet-stream`. A Depth 1 PROPFIND used to call `Tree.Open` on every extensionless file it listed.
- `davfs`: a PROPFIND now calls `Tree.Stat` at most once per entry; the children of a listed folder come from its `Tree.List` result. A Depth 1 PROPFIND used to stat each entry three or four times.
- `davfs`: a PUT onto an existing folder now answers 405 and leaves the folder in place. It used to replace the folder with a file, orphaning its contents.
- `davfs`: LOCK on the mount root now answers 403, and every lock timeout, `Infinite` or absent included, is capped by the new `Options.LockTimeout` (zero means one hour). A single root lock used to block every PUT, DELETE and LOCK from other clients until the process restarted.
- `blob/local`: `Put` now refuses a key with a segment starting with `.bizkit-tmp-` with `ErrForbidden`. It used to store the object, which `Stat` and `Open` found but `List` never returned.
- `davfs`: a PROPFIND, PROPPATCH or LOCK body over 1 MiB now answers 413, whether its length is declared or streamed. It used to be decoded whole, whatever its size.
- `davfs`: a Depth 1 PROPFIND no longer opens file contents or lists subdirectories; the Tree reader opens on the first read and a directory lists on the first Readdir. It used to call `Tree.Open` on every listed file, a GET per file on `blob/s3`.
- `davfs`: PROPFIND with `Depth: infinity`, or with no Depth header, now answers 403 with the `propfind-finite-depth` precondition. It used to walk every object under the mount in one request.
- `davfs`: MOVE or COPY onto a folder that contains the source now answers 403. It used to delete that folder, source included, before failing (MOVE) or replace it with the copied file (COPY).

## [0.2.0] - 2026-11-01

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
