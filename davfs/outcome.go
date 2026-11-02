package davfs

import (
	"context"
	"errors"
	"io"
	"net/http"
	"sync"

	"github.com/arhuman/bizkit"
	"github.com/arhuman/bizkit/fileserve"
)

type outcomeKey struct{}

// outcome carries, per request, the status a Tree error wrapping
// ErrForbidden or ErrTooLarge, or an XML body over maxXMLBody, calls for. x/net/webdav picks its own status for
// a failed FileSystem call (405 for any failed PUT, for instance), so
// statusWriter replaces it with this one.
//
// It also caches the entries the request has already statted or listed, by
// name: a PROPFIND stats every entry it reports three or four times. Any
// change to the Tree drops the cache.
type outcome struct {
	mu      sync.Mutex
	status  int
	entries map[string]Entry
}

func outcomeOf(ctx context.Context) *outcome {
	out, _ := ctx.Value(outcomeKey{}).(*outcome)
	return out
}

func (o *outcome) entry(name string) (Entry, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	e, ok := o.entries[name]
	return e, ok
}

func (o *outcome) remember(name string, e Entry) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.entries == nil {
		o.entries = map[string]Entry{}
	}
	o.entries[name] = e
}

func (o *outcome) forget() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.entries = nil
}

// record notes the status err maps to, keeping the first one.
func record(ctx context.Context, err error) {
	out := outcomeOf(ctx)
	if out == nil {
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
//
// When served is set, a successful status first gets the fileserve policy
// headers for the Content-Type http.ServeContent chose, so stored HTML or SVG is never
// rendered as a page on the host's origin.
type statusWriter struct {
	http.ResponseWriter
	out       *outcome
	rewritten int
	served    string
}

func (w *statusWriter) WriteHeader(code int) {
	if s := w.out.get(); code >= http.StatusBadRequest && s != 0 && s != code {
		code, w.rewritten = s, s
	}
	if w.served != "" && code >= http.StatusOK && code < http.StatusMultipleChoices {
		h := w.Header()
		fileserve.SetPolicy(h, w.served, h.Get("Content-Type"))
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
