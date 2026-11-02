# 1. blob.Backend lists by page with a key cursor

Date: 2026-11-02

## Status

Accepted

## Context

`blob.Backend.List(ctx, prefix)` returns every object under a prefix as one
sorted slice. Both backends build the full slice before returning it: blob/s3
drains `ListObjects`, blob/local walks the whole subtree, then each sorts. A
host listing a large workspace holds every `Info` in memory at once, and an
HTTP listing has no way to serve the result a page at a time.

The contract promises results sorted by key. On blob/local that order comes
only from the final sort: `filepath.WalkDir` visits each directory in name
order, which is not full-key order (the key `a-b` sorts before `a/x`, yet the
walk visits the directory `a` before the file `a-b`).

The only production caller outside bizkit is easybiz's
`BlobFileService.List`, which passes the slice through. yvocloud does not
import `blob`. Neither host implements `blob.Backend`.

## Decision

`blob.Backend` gains a paged listing method:

```go
ListPage(ctx context.Context, prefix, after string, limit int) ([]Info, string, error)
```

- It returns at most `limit` objects whose key starts with `prefix` and is
  strictly greater than `after`, in ascending byte order of the key
  (`strings.Compare`). `after == ""` starts at the beginning. Prefix matching
  is on the key string, as for `List`.
- `limit <= 0` means `blob.DefaultPageSize`, 1000, the S3 page maximum.
- The second result is the cursor for the next call: the key of the last
  returned object when more objects follow, `""` when this page is the last.
  A backend finds out by looking one object past `limit`, so a final page
  never costs an extra empty call.
- The cursor is a key, so pages are stateless: no token, no server-side
  session, safe to pass through a URL. Between pages, an object put after
  the cursor appears, one put before it is missed, and no object is
  returned twice.
- `List` stays on the interface with its current behavior, documented as
  reading the whole prefix into memory and meant for prefixes known to be
  small. A backend may implement it by looping over `ListPage`.
- blob/s3 maps `after` to `ListObjectsOptions.StartAfter` and stops reading
  after `limit + 1` objects. S3 already lists in byte order of the key.
- blob/local walks the subtree keeping only the `limit + 1` smallest keys
  greater than `after`, so memory is bounded by the page, not the prefix. It
  may skip a directory whose every key sorts at or before `after`.
- The blobtest suite gains a `ListPaged` case run by every backend: paging
  through a prefix with a small limit yields exactly `List`'s result, in
  order, with an empty cursor on the last page, and keys like `a-b` and
  `a/x` come back in byte order.

## Consequences

### Positive
- A host lists any prefix with memory bounded by the page size.
- A paged HTTP listing maps onto `ListPage` directly, with the cursor in the
  query string.
- The byte-order contract is now explicit and tested on both backends.

### Negative
- Adding a method breaks any `blob.Backend` implementation outside bizkit.
  None exists today; the CHANGELOG still flags it and the release is a minor
  bump.
- blob/local walks the subtree once per page, so paging through n objects
  costs more than one `List` call. Directory skipping reduces it; an index
  would remove it, at the price of state bizkit does not keep.

### Risks
- A caller that stores a cursor across a rename sees the order of the new
  keys, not the old ones. The cursor is a position, not a snapshot.

## Alternatives Considered

### Option 1: Iterator, `List(ctx, prefix) iter.Seq2[Info, error]`
- Pros: lowest memory, idiomatic since Go 1.23.
- Cons: blob/local cannot keep the key order without buffering the whole
  prefix, which brings the memory problem back; it cannot resume across HTTP
  requests; it replaces `List`, so every caller changes.

### Option 2: Limit only, `List(ctx, prefix, limit int)`
- Pros: smallest change.
- Cons: caps the result without a way to reach the next objects, so a large
  prefix becomes unreachable rather than paged.

### Option 3: Replace `List` with `ListPage`
- Pros: one listing method per backend.
- Cons: breaks easybiz for no gain; a whole-prefix convenience stays useful
  for small prefixes.

## References

- review_bizkit_20261102, blob/blob.go:65 (Medium, performance)
- [ListObjectsV2 StartAfter](https://docs.aws.amazon.com/AmazonS3/latest/API/API_ListObjectsV2.html)
