// Package davfs serves WebDAV over a host-provided Tree.
//
// The protocol is handled by golang.org/x/net/webdav, but no x/net type
// appears in the exported API: New returns a plain http.Handler and the host
// implements Tree, a namespace and content contract small enough to sit on a
// filesystem, on a blob.Backend plus a folder table, or on a parent_id tree in
// a database.
//
// davfs owns the protocol-level guarantees every host needs:
//
//   - Scope: the request path and the MOVE/COPY Destination must stay under
//     Options.Prefix. A dot segment ("." or ".." after percent-decoding), a NUL
//     byte, a Destination on another host or outside the prefix, or a
//     Destination inside the source's own subtree is refused with 403 before
//     the Tree is touched. The Tree therefore only ever sees clean,
//     slash-rooted names ("/" is the mount root).
//   - Atomic PUT: the body streams into Tree.Put, and a body that fails or is
//     cut short makes Put's reader fail instead of reaching io.EOF, so the
//     Tree never commits a truncated upload.
//   - Status mapping: a Tree error wrapping bizkit.ErrNotFound answers 404
//     (409 where WebDAV requires it, such as a missing parent), one wrapping
//     bizkit.ErrForbidden answers 403 and one wrapping bizkit.ErrTooLarge
//     answers 413. Options.MaxFileSize answers 413 too, before reading a body
//     whose declared length is over it, and while streaming otherwise.
//   - Bounded XML bodies: a PROPFIND, PROPPATCH or LOCK body over 1 MiB
//     answers 413, before reading a body whose declared length is over it,
//     and while streaming otherwise.
//   - Bounded listing: a PROPFIND with Depth infinity, or with no Depth header
//     (which RFC 4918 reads as infinity), answers 403 with the
//     propfind-finite-depth precondition instead of walking the whole mount.
//
// The host keeps everything tied to its users and data: authentication (and
// any credential caching), mapping a principal to a Tree root, per-path
// permissions through Options.Authorize, listing filters, trash or soft
// delete, version history and side channels such as a ".versions" route,
// which the host dispatches before this handler. Locks are held in memory per
// handler, so they do not survive a restart nor span several processes.
package davfs

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/webdav"

	"github.com/arhuman/bizkit"
)

// Entry describes a file or directory of a Tree.
type Entry struct {
	// Name is the last element of the entry's path, never a full path.
	Name string
	// Size is the content length in bytes; it is ignored for directories.
	Size    int64
	ModTime time.Time
	IsDir   bool
}

// Tree is the namespace and content a WebDAV mount serves.
//
// Every name is clean and slash-rooted, such as "/docs/a.txt"; "/" is the
// mount root, which davfs always treats as an existing directory and never
// asks a Tree to Stat, create, write, remove or rename. Errors for a missing
// name (or a missing parent) must wrap bizkit.ErrNotFound; errors wrapping
// bizkit.ErrForbidden or bizkit.ErrTooLarge are answered 403 and 413. The ctx
// is the request context, so a Tree can filter or scope per principal.
// Implementations must be safe for concurrent use.
type Tree interface {
	// Stat returns the entry at name.
	Stat(ctx context.Context, name string) (Entry, error)
	// List returns the direct children of the directory dir.
	List(ctx context.Context, dir string) ([]Entry, error)
	// Open returns the content of the file at name. It must be seekable so
	// GET can serve Range requests. The caller closes it.
	Open(ctx context.Context, name string) (io.ReadSeekCloser, error)
	// Put stores the content read from r at name, replacing any file there.
	// It must commit only after r returns io.EOF: when a read of r fails, Put
	// returns an error and leaves name exactly as it was. davfs has already
	// checked that the parent directory exists.
	Put(ctx context.Context, name string, r io.Reader) error
	// Mkdir creates the directory name. Its parent must exist.
	Mkdir(ctx context.Context, name string) error
	// Remove deletes name and, for a directory, everything under it.
	Remove(ctx context.Context, name string) error
	// Rename moves oldName, with its subtree, to newName. davfs has already
	// removed an existing newName when the client asked to overwrite it.
	Rename(ctx context.Context, oldName, newName string) error
}

// maxXMLBody caps a PROPFIND, PROPPATCH or LOCK body, which x/net/webdav
// decodes whole: those bodies name properties and a lock owner, never content.
const maxXMLBody = 1 << 20

// Op is the kind of access a request needs on a name, passed to
// Options.Authorize.
type Op int

const (
	// OpRead covers GET, HEAD, POST, OPTIONS, PROPFIND, UNLOCK and a COPY source.
	OpRead Op = iota + 1
	// OpWrite covers PUT, MKCOL, PROPPATCH, LOCK and every MOVE or COPY Destination.
	OpWrite
	// OpDelete covers DELETE and a MOVE source.
	OpDelete
)

// Options configures New.
type Options struct {
	// Prefix is the URL path the handler is mounted under, such as "/dav".
	// Empty mounts it at the root. A request outside Prefix answers 403.
	Prefix string
	// MaxFileSize rejects a PUT or COPY body longer than this many bytes with
	// 413. Zero means unlimited.
	MaxFileSize int64
	// Authorize, when set, is called before the Tree is touched with the
	// request name and, for MOVE and COPY, again with the Destination name
	// and OpWrite. An error wrapping bizkit.ErrForbidden answers 403, one
	// wrapping bizkit.ErrNotFound 404, any other 500. It is not called for
	// the children a PROPFIND or COPY walks: a Tree filters those itself.
	Authorize func(ctx context.Context, op Op, name string) error
	// Logger, when set, receives every request with the protocol error, if any.
	Logger func(r *http.Request, err error)
}

// Methods lists the HTTP methods the handler serves, for routers that
// register methods explicitly: WebDAV extension methods (MKCOL, PROPFIND...)
// are not part of a router's usual "any method" set.
var Methods = []string{
	http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut,
	http.MethodDelete, http.MethodOptions,
	"MKCOL", "COPY", "MOVE", "LOCK", "UNLOCK", "PROPFIND", "PROPPATCH",
}

// New returns a WebDAV handler serving tree under opts.Prefix.
func New(tree Tree, opts Options) http.Handler {
	prefix := strings.TrimSuffix(opts.Prefix, "/")
	return &handler{
		dav: &webdav.Handler{
			Prefix:     prefix,
			FileSystem: &fileSystem{tree: tree, maxSize: opts.MaxFileSize},
			LockSystem: webdav.NewMemLS(),
			Logger:     opts.Logger,
		},
		prefix:    prefix,
		maxSize:   opts.MaxFileSize,
		authorize: opts.Authorize,
	}
}

type handler struct {
	dav       *webdav.Handler
	prefix    string
	maxSize   int64
	authorize func(ctx context.Context, op Op, name string) error
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	name, ok := h.scoped(r.URL.Path)
	if !ok {
		writeStatus(w, http.StatusForbidden)
		return
	}
	dest := ""
	if r.Method == "MOVE" || r.Method == "COPY" {
		d, status := h.destination(r, name)
		if status != 0 {
			writeStatus(w, status)
			return
		}
		dest = d
	}
	if err := h.authorizeRequest(r.Context(), r.Method, name, dest); err != nil {
		writeStatus(w, statusOf(err))
		return
	}
	if h.refused(w, r) {
		return
	}

	out := new(outcome)
	r2 := r.Clone(context.WithValue(r.Context(), outcomeKey{}, out))
	r2.URL.Path, r2.URL.RawPath = h.prefix+name, ""
	if dest != "" {
		r2.Header.Set("Destination", h.prefix+dest)
	}
	if xmlMethod(r.Method) {
		r2.Body = xmlBody{ReadCloser: http.MaxBytesReader(w, r.Body, maxXMLBody), out: out}
	}
	h.dav.ServeHTTP(&statusWriter{ResponseWriter: w, out: out}, r2)
}

// refused answers a request davfs rejects from its headers alone, before
// x/net/webdav reads a body or touches the Tree, and reports whether it did.
func (h *handler) refused(w http.ResponseWriter, r *http.Request) bool {
	switch {
	case r.Method == http.MethodPut && h.maxSize > 0 && r.ContentLength > h.maxSize:
		writeStatus(w, http.StatusRequestEntityTooLarge)
	case xmlMethod(r.Method) && r.ContentLength > maxXMLBody:
		writeStatus(w, http.StatusRequestEntityTooLarge)
	case r.Method == "PROPFIND" && infiniteDepth(r.Header):
		writeFiniteDepthError(w)
	default:
		return false
	}
	return true
}

func xmlMethod(method string) bool {
	return method == "PROPFIND" || method == "PROPPATCH" || method == "LOCK"
}

// xmlBody records 413 when its body goes over maxXMLBody, so statusWriter
// replaces the 400 x/net/webdav answers for an XML body that fails to read.
type xmlBody struct {
	io.ReadCloser
	out *outcome
}

func (b xmlBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
		b.out.set(http.StatusRequestEntityTooLarge)
	}
	return n, err
}

// scoped maps a decoded URL path to its clean Tree name, reporting false when
// it lies outside the prefix or carries a dot segment or a NUL byte.
func (h *handler) scoped(p string) (string, bool) {
	rest, ok := strings.CutPrefix(p, h.prefix)
	if !ok || (rest != "" && rest[0] != '/') {
		return "", false
	}
	for seg := range strings.SplitSeq(rest, "/") {
		if seg == "." || seg == ".." || strings.ContainsRune(seg, 0) {
			return "", false
		}
	}
	return path.Clean("/" + rest), true
}

// destination returns the clean Tree name of a MOVE or COPY Destination, or
// the status refusing it.
func (h *handler) destination(r *http.Request, src string) (string, int) {
	hdr := r.Header.Get("Destination")
	if hdr == "" {
		return "", http.StatusBadRequest
	}
	u, err := url.Parse(hdr)
	if err != nil {
		return "", http.StatusBadRequest
	}
	if u.Host != "" && u.Host != r.Host {
		return "", http.StatusForbidden
	}
	dst, ok := h.scoped(u.Path)
	if !ok || overlaps(src, dst) {
		return "", http.StatusForbidden
	}
	return dst, 0
}

// overlaps reports whether one of two clean Tree names is the other or lies
// under it. An overwriting MOVE or COPY deletes its destination first, so a
// destination containing the source would destroy it.
func overlaps(a, b string) bool {
	if a == "/" || b == "/" || a == b {
		return true
	}
	return strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/")
}

func (h *handler) authorizeRequest(ctx context.Context, method, name, dest string) error {
	if h.authorize == nil {
		return nil
	}
	if err := h.authorize(ctx, sourceOp(method), name); err != nil {
		return err
	}
	if dest != "" {
		return h.authorize(ctx, OpWrite, dest)
	}
	return nil
}

func sourceOp(method string) Op {
	switch method {
	case http.MethodPut, "MKCOL", "PROPPATCH", "LOCK":
		return OpWrite
	case http.MethodDelete, "MOVE":
		return OpDelete
	default:
		return OpRead
	}
}

func statusOf(err error) int {
	switch {
	case errors.Is(err, bizkit.ErrForbidden):
		return http.StatusForbidden
	case errors.Is(err, bizkit.ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, bizkit.ErrTooLarge):
		return http.StatusRequestEntityTooLarge
	default:
		return http.StatusInternalServerError
	}
}

func writeStatus(w http.ResponseWriter, status int) {
	http.Error(w, http.StatusText(status), status)
}

// infiniteDepth reports whether a PROPFIND asks for the whole subtree: RFC
// 4918 reads an absent Depth header as infinity.
func infiniteDepth(hdr http.Header) bool {
	d, ok := hdr["Depth"]
	return !ok || len(d) == 0 || strings.EqualFold(strings.TrimSpace(d[0]), "infinity")
}

// writeFiniteDepthError answers 403 with the propfind-finite-depth
// precondition of RFC 4918 section 9.1.
func writeFiniteDepthError(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.WriteHeader(http.StatusForbidden)
	_, _ = io.WriteString(w, `<?xml version="1.0" encoding="utf-8"?>`+"\n"+
		`<D:error xmlns:D="DAV:"><D:propfind-finite-depth/></D:error>`)
}

type outcomeKey struct{}

// outcome carries, per request, the status a Tree error wrapping
// ErrForbidden or ErrTooLarge, or an XML body over maxXMLBody, calls for. x/net/webdav picks its own status for
// a failed FileSystem call (405 for any failed PUT, for instance), so
// statusWriter replaces it with this one.
type outcome struct {
	mu     sync.Mutex
	status int
}

// record notes the status err maps to, keeping the first one.
func record(ctx context.Context, err error) {
	out, ok := ctx.Value(outcomeKey{}).(*outcome)
	if !ok {
		return
	}
	status := 0
	switch {
	case errors.Is(err, bizkit.ErrForbidden):
		status = http.StatusForbidden
	case errors.Is(err, bizkit.ErrTooLarge):
		status = http.StatusRequestEntityTooLarge
	default:
		return
	}
	out.set(status)
}

// set keeps the first status recorded.
func (o *outcome) set(status int) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.status == 0 {
		o.status = status
	}
}

func (o *outcome) get() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.status
}

// statusWriter rewrites an error status to the recorded outcome, and the
// status text x/net/webdav writes after it, so status and body agree.
type statusWriter struct {
	http.ResponseWriter
	out       *outcome
	rewritten int
}

func (w *statusWriter) WriteHeader(code int) {
	if s := w.out.get(); code >= http.StatusBadRequest && s != 0 && s != code {
		code, w.rewritten = s, s
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(p []byte) (int, error) {
	if w.rewritten == 0 {
		return w.ResponseWriter.Write(p)
	}
	status := w.rewritten
	w.rewritten = 0
	if _, err := io.WriteString(w.ResponseWriter, http.StatusText(status)); err != nil {
		return 0, err
	}
	return len(p), nil
}

// Unwrap exposes the underlying writer to http.ResponseController.
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
