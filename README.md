# bizkit

Stateless Go building blocks shared by EasyBiz and yvocloud: byte storage backends, safe file
serving and uploads, share-link policy, a calendar grid and ICS feed, and gin adapters.

The module is `net/http`-native. It owns no tables and no transactions: hosts keep their own
persistence and domain model and call bizkit for byte-level and policy-level logic.

```sh
go get github.com/arhuman/bizkit
```

## Packages

| Package | What it provides |
|---------|------------------|
| `bizkit` (module root) | The sentinel errors every package wraps. |
| `blob` | The `Backend` byte storage contract: `Put`, `Open`, `Stat`, `Delete` and `List` over slash-separated keys. |
| `blob/blobtest` | The conformance suite every `blob.Backend` must pass (`blobtest.Run`). |
| `blob/local` | Filesystem backend: plain files under a root directory, keys sanitized so none resolves outside it. It does not persist the content type. |
| `blob/s3` | Backend for S3 and S3-compatible stores (MinIO, Garage, AWS S3), under an optional key prefix in an existing bucket. |
| `fileserve` | Safe file serving (inline allowlist, attachment otherwise, Range, nosniff, RFC 5987 filename), content sniffing and a capped multipart upload reader. |
| `sharelink` | Stateless share-link policy: token generation and hashing, PIN hashing and lockout, expiry, upload caps and the `Sealer` contract. The host stores the results. |
| `calendar` | A month grid and an upcoming-events agenda over host-supplied events; the caller passes the location and current time. |
| `calendar/ics` | A byte-stable RFC 5545 feed writer and an HTTP feed handler behind a host-resolved token. |
| `ginx` | Mounts `net/http` handlers in gin, copying gin's request keys into the request context. |
| `davfs` | A WebDAV `http.Handler` over a host-implemented `Tree`, with path scoping, atomic PUT, status mapping and bounded bodies and listings. |

## Usage

Store an upload with `blob/local` and serve it back with `fileserve`, mapping a missing key to 404:

```go
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"strings"

	"github.com/arhuman/bizkit"
	"github.com/arhuman/bizkit/blob"
	"github.com/arhuman/bizkit/blob/local"
	"github.com/arhuman/bizkit/fileserve"
)

func main() {
	store, err := local.New("/var/lib/myapp/files")
	if err != nil {
		log.Fatal(err)
	}

	_, err = store.Put(context.Background(), "docs/hello.txt", strings.NewReader("hello"),
		blob.PutOptions{ContentType: "text/plain", MaxSize: fileserve.DefaultMaxFileSize})
	if err != nil {
		log.Fatal(err)
	}

	http.HandleFunc("GET /files/{key...}", func(w http.ResponseWriter, r *http.Request) {
		obj, err := store.Open(r.Context(), r.PathValue("key"))
		if errors.Is(err, bizkit.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		if err != nil {
			http.Error(w, "storage error", http.StatusInternalServerError)
			return
		}
		defer obj.Close()
		fileserve.ServeObject(w, r, "", obj)
	})
	log.Fatal(http.ListenAndServe(":8080", nil))
}
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

Releases are cut with `make release`: the version is derived from Conventional Commits and the release is gated on `make ci`.
