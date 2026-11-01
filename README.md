# bizkit

Stateless Go building blocks shared by EasyBiz and yvocloud: byte storage backends, safe file
serving and uploads, share-link policy, a calendar grid and ICS feed, and gin adapters.

The module is `net/http`-native. It owns no tables and no transactions: hosts keep their own
persistence and domain model and call bizkit for byte-level and policy-level logic.

```sh
go get github.com/arhuman/bizkit
```

## Errors

Packages wrap the sentinel errors in `errors.go` (`ErrNotFound`, `ErrTooLarge`, `ErrForbidden`,
`ErrExpired`, `ErrLocked`). Match them with `errors.Is` and translate them into your own errors.

## Dependency boundaries

`internal/archtest` fails the build when a package imports gin outside `ginx`, the S3 client
outside `blob/s3`, or any SQL driver anywhere.

## Development

```sh
make test   # short unit tests
make ci     # tidy, audit (gofmt, lint, govulncheck, coverage gate), race tests
```

Releases are tagged `v0.x` by hand.
