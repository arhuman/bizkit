package davfs_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/arhuman/bizkit"
	"github.com/arhuman/bizkit/davfs"
)

func fixture() *memTree {
	return newMemTree(map[string]string{
		"/a.txt":        "hello",
		"/dir/":         "",
		"/dir/in.txt":   "inside",
		"/secret/":      "",
		"/secret/k.txt": "key",
	})
}

var fixtureSnapshot = []string{
	"/a.txt=hello",
	"/dir/",
	"/dir/in.txt=inside",
	"/secret/",
	"/secret/k.txt=key",
}

type result struct {
	code int
	body string
}

func do(t *testing.T, h http.Handler, method, target string, body io.Reader, header map[string]string) result {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), method, target, body)
	for k, v := range header {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return result{code: rec.Code, body: rec.Body.String()}
}

func assertSnapshot(t *testing.T, tree *memTree, want []string) {
	t.Helper()
	if got := tree.snapshot(); !slices.Equal(got, want) {
		t.Fatalf("tree = %q, want %q", got, want)
	}
}

// escapingDestinations all leave the "/dav" scope, by dot segments (plain
// and percent-encoded), another host, another prefix or a relative reference.
var escapingDestinations = []string{
	"/dav/../outside.txt",
	"/dav/%2e%2e/outside.txt",
	"/dav/%2E%2E/dav/b.txt",
	"/dav/dir/%2e%2e/%2e%2e/outside.txt",
	"/dav/./b.txt",
	"http://evil.example/dav/b.txt",
	"https://evil.example:8443/dav/b.txt",
	"/other/b.txt",
	"/davx/b.txt",
	"../b.txt",
	"b.txt",
	"/dav/dir/%00.txt",
}

func TestMove_DestinationOutsideScopeForbidden(t *testing.T) {
	for _, dest := range escapingDestinations {
		t.Run(dest, func(t *testing.T) {
			tree := fixture()
			h := davfs.New(tree, davfs.Options{Prefix: "/dav"})
			res := do(t, h, "MOVE", "/dav/a.txt", nil, map[string]string{"Destination": dest})
			if res.code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403", res.code)
			}
			assertSnapshot(t, tree, fixtureSnapshot)
			if n := tree.callCount(); n != 0 {
				t.Fatalf("tree calls = %d, want 0", n)
			}
		})
	}
}

func TestMove_IntoOwnSubtreeForbidden(t *testing.T) {
	tree := fixture()
	h := davfs.New(tree, davfs.Options{Prefix: "/dav"})
	for _, tc := range []struct{ src, dst string }{
		{"/dav/dir", "/dav/dir/sub"},
		{"/dav/dir", "/dav/dir"},
		{"/dav/", "/dav/x"},
	} {
		if res := do(t, h, "MOVE", tc.src, nil, map[string]string{"Destination": tc.dst}); res.code != http.StatusForbidden {
			t.Fatalf("MOVE %s -> %s status = %d, want 403", tc.src, tc.dst, res.code)
		}
	}
	assertSnapshot(t, tree, fixtureSnapshot)
}

// An overwriting MOVE or COPY deletes its destination first, so a destination
// containing the source would take the source with it.
func TestMoveCopy_OntoAncestorForbidden(t *testing.T) {
	for _, method := range []string{"MOVE", "COPY"} {
		for _, dst := range []string{"/dav/dir", "/dav/dir/", "/dav/", "/dav"} {
			t.Run(method+" "+dst, func(t *testing.T) {
				tree := fixture()
				h := davfs.New(tree, davfs.Options{Prefix: "/dav"})
				res := do(t, h, method, "/dav/dir/in.txt", nil, map[string]string{"Destination": dst, "Overwrite": "T"})
				if res.code != http.StatusForbidden {
					t.Errorf("status = %d, want 403", res.code)
				}
				assertSnapshot(t, tree, fixtureSnapshot)
			})
		}
	}
}

func TestMove_MissingOrMalformedDestination(t *testing.T) {
	h := davfs.New(fixture(), davfs.Options{Prefix: "/dav"})
	if res := do(t, h, "MOVE", "/dav/a.txt", nil, nil); res.code != http.StatusBadRequest {
		t.Fatalf("no Destination status = %d, want 400", res.code)
	}
	if res := do(t, h, "MOVE", "/dav/a.txt", nil, map[string]string{"Destination": "http://[::1"}); res.code != http.StatusBadRequest {
		t.Fatalf("malformed Destination status = %d, want 400", res.code)
	}
}

func TestMove_WithinScope(t *testing.T) {
	tree := fixture()
	h := davfs.New(tree, davfs.Options{Prefix: "/dav/"})
	res := do(t, h, "MOVE", "/dav/a.txt", nil, map[string]string{"Destination": "http://example.com/dav/dir/b.txt"})
	if res.code != http.StatusCreated {
		t.Fatalf("status = %d, want 201", res.code)
	}
	assertSnapshot(t, tree, []string{"/dir/", "/dir/b.txt=hello", "/dir/in.txt=inside", "/secret/", "/secret/k.txt=key"})

	res = do(t, h, "MOVE", "/dav/dir/b.txt", nil, map[string]string{"Destination": "/dav/dir/in.txt", "Overwrite": "T"})
	if res.code != http.StatusNoContent {
		t.Fatalf("overwrite status = %d, want 204", res.code)
	}
	assertSnapshot(t, tree, []string{"/dir/", "/dir/in.txt=hello", "/secret/", "/secret/k.txt=key"})
}

func TestMove_AuthorizeChecksSourceAndDestination(t *testing.T) {
	tree := fixture()
	var seen []string
	h := davfs.New(tree, davfs.Options{Prefix: "/dav", Authorize: func(_ context.Context, op davfs.Op, name string) error {
		seen = append(seen, fmt.Sprintf("%d %s", op, name))
		if op == davfs.OpWrite && strings.HasPrefix(name, "/secret/") {
			return fmt.Errorf("no grant: %w", bizkit.ErrForbidden)
		}
		return nil
	}})
	res := do(t, h, "MOVE", "/dav/a.txt", nil, map[string]string{"Destination": "/dav/secret/a.txt"})
	if res.code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", res.code)
	}
	if want := []string{"3 /a.txt", "2 /secret/a.txt"}; !slices.Equal(seen, want) {
		t.Fatalf("authorize calls = %q, want %q", seen, want)
	}
	assertSnapshot(t, tree, fixtureSnapshot)
}

func TestCopy_DestinationOutsideScopeForbidden(t *testing.T) {
	for _, dest := range escapingDestinations {
		t.Run(dest, func(t *testing.T) {
			tree := fixture()
			h := davfs.New(tree, davfs.Options{Prefix: "/dav"})
			res := do(t, h, "COPY", "/dav/a.txt", nil, map[string]string{"Destination": dest})
			if res.code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403", res.code)
			}
			assertSnapshot(t, tree, fixtureSnapshot)
		})
	}
}

func TestCopy_WithinScope(t *testing.T) {
	tree := fixture()
	h := davfs.New(tree, davfs.Options{Prefix: "/dav"})
	if res := do(t, h, "COPY", "/dav/a.txt", nil, map[string]string{"Destination": "/dav/dir/copy.txt"}); res.code != http.StatusCreated {
		t.Fatalf("file status = %d, want 201", res.code)
	}
	if res := do(t, h, "COPY", "/dav/dir", nil, map[string]string{"Destination": "/dav/dir2"}); res.code != http.StatusCreated {
		t.Fatalf("dir status = %d, want 201", res.code)
	}
	assertSnapshot(t, tree, []string{
		"/a.txt=hello",
		"/dir/", "/dir/copy.txt=hello", "/dir/in.txt=inside",
		"/dir2/", "/dir2/copy.txt=hello", "/dir2/in.txt=inside",
		"/secret/", "/secret/k.txt=key",
	})
}

func TestCopy_OverMaxFileSizeIsNotCommitted(t *testing.T) {
	tree := fixture()
	h := davfs.New(tree, davfs.Options{Prefix: "/dav", MaxFileSize: 4})
	res := do(t, h, "COPY", "/dav/a.txt", nil, map[string]string{"Destination": "/dav/b.txt"})
	if res.code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", res.code)
	}
	assertSnapshot(t, tree, fixtureSnapshot)
}

// failingBody yields its data, then fails as a client hanging up would.
type failingBody struct {
	data string
	sent bool
}

func (b *failingBody) Read(p []byte) (int, error) {
	if b.sent {
		return 0, io.ErrUnexpectedEOF
	}
	b.sent = true
	return copy(p, b.data), nil
}

func TestPut_InterruptedLeavesNoObject(t *testing.T) {
	tree := fixture()
	h := davfs.New(tree, davfs.Options{Prefix: "/dav"})

	res := do(t, h, http.MethodPut, "/dav/new.txt", &failingBody{data: "partial"}, nil)
	if res.code != http.StatusMethodNotAllowed {
		t.Fatalf("new file status = %d, want 405", res.code)
	}
	res = do(t, h, http.MethodPut, "/dav/a.txt", &failingBody{data: "trunc"}, nil)
	if res.code != http.StatusMethodNotAllowed {
		t.Fatalf("overwrite status = %d, want 405", res.code)
	}
	assertSnapshot(t, tree, fixtureSnapshot)
}

func TestPut_OntoCollectionRefused(t *testing.T) {
	tree := fixture()
	h := davfs.New(tree, davfs.Options{Prefix: "/dav"})
	for _, target := range []string{"/dav/dir", "/dav/dir/"} {
		res := do(t, h, http.MethodPut, target, strings.NewReader("x"), nil)
		if res.code != http.StatusMethodNotAllowed {
			t.Fatalf("PUT %s status = %d, want 405", target, res.code)
		}
	}
	assertSnapshot(t, tree, fixtureSnapshot)
}

func TestPut_OverMaxFileSizeReturns413(t *testing.T) {
	for name, length := range map[string]int64{"declared": 6, "chunked": -1} {
		t.Run(name, func(t *testing.T) {
			tree := fixture()
			h := davfs.New(tree, davfs.Options{Prefix: "/dav", MaxFileSize: 5})
			body := strings.NewReader("123456")
			req := httptest.NewRequestWithContext(t.Context(), http.MethodPut, "/dav/big.txt", struct{ io.Reader }{body})
			req.ContentLength = length
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != http.StatusRequestEntityTooLarge {
				t.Fatalf("status = %d, want 413", rec.Code)
			}
			if got := strings.TrimSpace(rec.Body.String()); got != "Request Entity Too Large" {
				t.Fatalf("body = %q, want %q", got, "Request Entity Too Large")
			}
			if length > 0 && body.Len() != 6 {
				t.Fatalf("declared oversize body was read: %d bytes left, want 6", body.Len())
			}
			assertSnapshot(t, tree, fixtureSnapshot)
		})
	}
}

func TestPut_AtMaxFileSizeIsStored(t *testing.T) {
	tree := newMemTree(nil)
	h := davfs.New(tree, davfs.Options{MaxFileSize: 5})
	if res := do(t, h, http.MethodPut, "/five.txt", strings.NewReader("12345"), nil); res.code != http.StatusCreated {
		t.Fatalf("status = %d, want 201", res.code)
	}
	assertSnapshot(t, tree, []string{"/five.txt=12345"})
}

func TestTreeSentinelsMapToStatus(t *testing.T) {
	tree := fixture()
	tree.deny["/dir/in.txt"] = true
	tree.quota = 3
	h := davfs.New(tree, davfs.Options{Prefix: "/dav"})

	for _, tc := range []struct {
		method, target, body string
		want                 int
	}{
		{http.MethodGet, "/dav/dir/in.txt", "", http.StatusForbidden},
		{"PROPFIND", "/dav/dir/in.txt", "", http.StatusForbidden},
		{http.MethodDelete, "/dav/dir/in.txt", "", http.StatusForbidden},
		{http.MethodPut, "/dav/dir/in.txt", "x", http.StatusForbidden},
		{http.MethodPut, "/dav/q.txt", "1234", http.StatusRequestEntityTooLarge},
		{http.MethodGet, "/dav/missing.txt", "", http.StatusNotFound},
	} {
		res := do(t, h, tc.method, tc.target, strings.NewReader(tc.body), nil)
		if res.code != tc.want {
			t.Fatalf("%s %s status = %d, want %d", tc.method, tc.target, res.code, tc.want)
		}
	}
	assertSnapshot(t, tree, fixtureSnapshot)
}

func TestAuthorize_StatusAndOps(t *testing.T) {
	var seen []string
	authErr := error(nil)
	h := davfs.New(fixture(), davfs.Options{Prefix: "/dav", Authorize: func(_ context.Context, op davfs.Op, name string) error {
		seen = append(seen, fmt.Sprintf("%d %s", op, name))
		return authErr
	}})
	for _, m := range davfs.Methods {
		do(t, h, m, "/dav/dir/", nil, map[string]string{"Destination": "/dav/x"})
	}
	want := []string{
		"1 /dir", "1 /dir", "1 /dir", "2 /dir", "3 /dir", "1 /dir",
		"2 /dir", "1 /dir", "2 /x", "3 /dir", "2 /x", "2 /dir", "1 /dir", "1 /dir", "2 /dir",
	}
	if !slices.Equal(seen, want) {
		t.Fatalf("authorize calls = %q, want %q", seen, want)
	}

	for err, want := range map[error]int{
		fmt.Errorf("x: %w", bizkit.ErrForbidden): http.StatusForbidden,
		fmt.Errorf("x: %w", bizkit.ErrNotFound):  http.StatusNotFound,
		fmt.Errorf("x: %w", bizkit.ErrTooLarge):  http.StatusRequestEntityTooLarge,
		errors.New("database down"):              http.StatusInternalServerError,
	} {
		authErr = err
		if res := do(t, h, http.MethodGet, "/dav/a.txt", nil, nil); res.code != want {
			t.Fatalf("authorize %v status = %d, want %d", err, res.code, want)
		}
	}
}

func TestBasics(t *testing.T) {
	tree := fixture()
	h := davfs.New(tree, davfs.Options{Prefix: "/dav"})

	steps := []struct {
		method, target, body string
		header               map[string]string
		want                 int
		wantBody             string
	}{
		{"MKCOL", "/dav/docs", "", nil, http.StatusCreated, ""},
		{"MKCOL", "/dav/docs", "", nil, http.StatusMethodNotAllowed, ""},
		{"MKCOL", "/dav/", "", nil, http.StatusMethodNotAllowed, ""},
		{"MKCOL", "/dav/missing/sub", "", nil, http.StatusConflict, ""},
		{http.MethodPut, "/dav/docs/n.txt", "note", nil, http.StatusCreated, ""},
		{http.MethodPut, "/dav/missing/n.txt", "note", nil, http.StatusConflict, ""},
		{http.MethodPut, "/dav/a.txt/n.txt", "note", nil, http.StatusConflict, ""},
		{http.MethodPut, "/dav/", "note", nil, http.StatusForbidden, ""},
		{http.MethodGet, "/dav/docs/n.txt", "", nil, http.StatusOK, "note"},
		{http.MethodGet, "/dav/a.txt", "", map[string]string{"Range": "bytes=1-3"}, http.StatusPartialContent, "ell"},
		{"PROPFIND", "/dav/docs/", "", map[string]string{"Depth": "1"}, http.StatusMultiStatus, ""},
		{"PROPFIND", "/dav/", "", map[string]string{"Depth": "1"}, http.StatusMultiStatus, ""},
		{http.MethodDelete, "/dav/docs/n.txt", "", nil, http.StatusNoContent, ""},
		{http.MethodGet, "/dav/docs/n.txt", "", nil, http.StatusNotFound, ""},
		{http.MethodDelete, "/dav/docs/n.txt", "", nil, http.StatusNotFound, ""},
		{http.MethodDelete, "/dav/", "", nil, http.StatusForbidden, ""},
		{http.MethodDelete, "/dav/docs", "", nil, http.StatusNoContent, ""},
	}
	for _, s := range steps {
		res := do(t, h, s.method, s.target, strings.NewReader(s.body), s.header)
		if res.code != s.want {
			t.Fatalf("%s %s status = %d, want %d (%s)", s.method, s.target, res.code, s.want, res.body)
		}
		if s.wantBody != "" && res.body != s.wantBody {
			t.Fatalf("%s %s body = %q, want %q", s.method, s.target, res.body, s.wantBody)
		}
	}
	assertSnapshot(t, tree, fixtureSnapshot)
}

func TestPropfind_ListsChildren(t *testing.T) {
	h := davfs.New(fixture(), davfs.Options{Prefix: "/dav"})
	res := do(t, h, "PROPFIND", "/dav/dir/", nil, map[string]string{"Depth": "1"})
	if res.code != http.StatusMultiStatus {
		t.Fatalf("status = %d, want 207", res.code)
	}
	for _, href := range []string{"<D:href>/dav/dir/</D:href>", "<D:href>/dav/dir/in.txt</D:href>"} {
		if !strings.Contains(res.body, href) {
			t.Fatalf("body lacks %s:\n%s", href, res.body)
		}
	}
	if strings.Contains(res.body, "/dav/a.txt") {
		t.Fatalf("body lists a sibling of the directory:\n%s", res.body)
	}
}

func TestTraversal_EveryMethodForbidden(t *testing.T) {
	targets := []string{
		"/dav/../secret.txt",
		"/dav/%2e%2e/secret.txt",
		"/dav/dir/%2E%2E/%2E%2E/etc/passwd",
		"/dav/dir/./in.txt",
		"/dav/dir/%00",
		"/other/a.txt",
		"/davx/a.txt",
	}
	for _, m := range davfs.Methods {
		for _, target := range targets {
			t.Run(m+" "+target, func(t *testing.T) {
				tree := fixture()
				h := davfs.New(tree, davfs.Options{Prefix: "/dav"})
				req := httptest.NewRequestWithContext(t.Context(), m, "/", strings.NewReader("x"))
				req.URL.Path = mustUnescape(t, target)
				req.Header.Set("Destination", "/dav/moved.txt")
				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, req)
				if rec.Code != http.StatusForbidden {
					t.Fatalf("status = %d, want 403", rec.Code)
				}
				if n := tree.callCount(); n != 0 {
					t.Fatalf("tree calls = %d, want 0", n)
				}
				assertSnapshot(t, tree, fixtureSnapshot)
			})
		}
	}
}

func mustUnescape(t *testing.T, s string) string {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://example.com"+s, nil)
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return req.URL.Path
}

func TestPropfind_InfiniteDepthForbidden(t *testing.T) {
	for _, header := range []map[string]string{nil, {"Depth": "infinity"}, {"Depth": "Infinity"}} {
		t.Run(fmt.Sprint(header), func(t *testing.T) {
			tree := fixture()
			h := davfs.New(tree, davfs.Options{Prefix: "/dav"})
			res := do(t, h, "PROPFIND", "/dav/", nil, header)
			if res.code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403", res.code)
			}
			if !strings.Contains(res.body, "<D:propfind-finite-depth/>") {
				t.Fatalf("body lacks the propfind-finite-depth precondition:\n%s", res.body)
			}
			if n := tree.callCount(); n != 0 {
				t.Fatalf("tree calls = %d, want 0", n)
			}
		})
	}
	for _, depth := range []string{"0", "1"} {
		res := do(t, davfs.New(fixture(), davfs.Options{Prefix: "/dav"}), "PROPFIND", "/dav/", nil, map[string]string{"Depth": depth})
		if res.code != http.StatusMultiStatus {
			t.Fatalf("Depth %s status = %d, want 207", depth, res.code)
		}
	}
}

func TestPropfind_ListingOpensNoContent(t *testing.T) {
	tree := fixture()
	h := davfs.New(tree, davfs.Options{Prefix: "/dav"})
	res := do(t, h, "PROPFIND", "/dav/", nil, map[string]string{"Depth": "1"})
	if res.code != http.StatusMultiStatus {
		t.Fatalf("status = %d, want 207", res.code)
	}
	if !strings.Contains(res.body, "<D:getcontentlength>5</D:getcontentlength>") {
		t.Fatalf("body lacks a.txt's length: %s", res.body)
	}
	if lists, opens := tree.listOpenCounts(); lists != 1 || opens != 0 {
		t.Fatalf("Depth 1 PROPFIND made %d List and %d Open, want 1 List and 0 Open", lists, opens)
	}
}

// xmlBodies wrap padding in a well-formed PROPFIND, PROPPATCH or LOCK body,
// so only its size can make the request fail.
var xmlBodies = []struct {
	method, target, head, tail string
}{
	{"PROPFIND", "/dav/", `<D:propfind xmlns:D="DAV:"><D:allprop/>`, `</D:propfind>`},
	{"PROPPATCH", "/dav/a.txt", `<D:propertyupdate xmlns:D="DAV:"><D:set><D:prop><Z:p xmlns:Z="urn:z">`, `</Z:p></D:prop></D:set></D:propertyupdate>`},
	{"LOCK", "/dav/a.txt", `<D:lockinfo xmlns:D="DAV:"><D:lockscope><D:exclusive/></D:lockscope><D:locktype><D:write/></D:locktype><D:owner>`, `</D:owner></D:lockinfo>`},
}

func TestXMLBody_OverLimitReturns413(t *testing.T) {
	header := map[string]string{"Depth": "0"}
	for _, b := range xmlBodies {
		over := b.head + strings.Repeat(" ", 2<<20) + b.tail
		for name, body := range map[string]func() io.Reader{
			"declared": func() io.Reader { return strings.NewReader(over) },
			"chunked":  func() io.Reader { return io.MultiReader(strings.NewReader(over)) },
		} {
			t.Run(b.method+"/"+name, func(t *testing.T) {
				res := do(t, davfs.New(fixture(), davfs.Options{Prefix: "/dav"}), b.method, b.target, body(), header)
				if res.code != http.StatusRequestEntityTooLarge {
					t.Fatalf("status = %d, want 413", res.code)
				}
			})
		}
		small := b.head + b.tail
		res := do(t, davfs.New(fixture(), davfs.Options{Prefix: "/dav"}), b.method, b.target, io.MultiReader(strings.NewReader(small)), header)
		if res.code == http.StatusRequestEntityTooLarge || res.code >= http.StatusInternalServerError {
			t.Fatalf("%s with a small body: status = %d", b.method, res.code)
		}
	}
}

const lockBody = `<D:lockinfo xmlns:D="DAV:"><D:lockscope><D:exclusive/></D:lockscope><D:locktype><D:write/></D:locktype></D:lockinfo>`

// lockTimeout sends a LOCK on /dav/a.txt and returns the timeout the
// response grants, failing unless the LOCK answers 200.
func lockTimeout(t *testing.T, h http.Handler, header map[string]string) string {
	t.Helper()
	var body io.Reader
	if header["If"] == "" {
		body = strings.NewReader(lockBody)
	}
	res := do(t, h, "LOCK", "/dav/a.txt", body, header)
	if res.code != http.StatusOK {
		t.Fatalf("LOCK status = %d, want 200 (%s)", res.code, res.body)
	}
	_, after, _ := strings.Cut(res.body, "<D:timeout>")
	granted, _, _ := strings.Cut(after, "</D:timeout>")
	return granted
}

func TestLock_RootRefusedAndTimeoutCapped(t *testing.T) {
	h := davfs.New(fixture(), davfs.Options{Prefix: "/dav"})
	root := do(t, h, "LOCK", "/dav/", strings.NewReader(lockBody), map[string]string{"Depth": "infinity", "Timeout": "Infinite"})
	if root.code != http.StatusForbidden {
		t.Fatalf("LOCK on the mount root: status = %d, want 403", root.code)
	}
	if res := do(t, h, http.MethodPut, "/dav/b.txt", strings.NewReader("x"), nil); res.code != http.StatusCreated {
		t.Fatalf("PUT after a refused root LOCK: status = %d, want 201", res.code)
	}

	cases := []struct {
		name, timeout, want string
		limit               time.Duration
	}{
		{"infinite, default cap", "Infinite", "Second-3600", 0},
		{"absent, default cap", "", "Second-3600", 0},
		{"over the cap", "Second-99999", "Second-60", time.Minute},
		{"infinite with fallback", "Infinite, Second-99999", "Second-60", time.Minute},
		{"under the cap", "Second-10", "Second-10", time.Minute},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := davfs.New(fixture(), davfs.Options{Prefix: "/dav", LockTimeout: c.limit})
			header := map[string]string{"Depth": "0"}
			if c.timeout != "" {
				header["Timeout"] = c.timeout
			}
			if got := lockTimeout(t, h, header); got != c.want {
				t.Fatalf("granted timeout = %q, want %q", got, c.want)
			}
		})
	}

	t.Run("refresh", func(t *testing.T) {
		h := davfs.New(fixture(), davfs.Options{Prefix: "/dav", LockTimeout: time.Minute})
		req := httptest.NewRequestWithContext(t.Context(), "LOCK", "/dav/a.txt", strings.NewReader(lockBody))
		req.Header.Set("Depth", "0")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		token := rec.Header().Get("Lock-Token")
		if rec.Code != http.StatusOK || token == "" {
			t.Fatalf("LOCK status = %d, Lock-Token = %q", rec.Code, token)
		}
		if got := lockTimeout(t, h, map[string]string{"If": "(" + token + ")", "Timeout": "Infinite"}); got != "Second-60" {
			t.Fatalf("refreshed timeout = %q, want Second-60", got)
		}
	})
}
