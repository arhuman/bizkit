package ics

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/arhuman/bizkit"
)

// Redacted replaces the feed token in paths returned by RedactPath.
const Redacted = "REDACTED"

const (
	icsSuffix    = ".ics"
	notFoundBody = "not found"
	// feedCacheControl keeps a token-bearing personal feed out of shared
	// caches and makes clients revalidate every poll, which the ETag makes
	// a cheap 304.
	feedCacheControl = "private, no-cache"
)

// Resolver returns the Feed a token grants. It must return an error wrapping
// bizkit.ErrNotFound, bizkit.ErrExpired or bizkit.ErrForbidden for an
// unknown, expired or revoked token; FeedHandler maps all three to the same
// 404 so a client cannot tell which. Any other error is a 500.
type Resolver func(ctx context.Context, token string) (Feed, error)

// FeedHandler serves the iCalendar feed of the token found in the last
// segment of the request path, an optional ".ics" suffix stripped, so it
// mounts as "GET /feeds/{token}.ics" or "GET /feeds/{token}" alike.
//
// GET and HEAD answer 200 with Content-Type text/calendar, a strong ETag
// (the SHA-256 of the body), Cache-Control "private, no-cache" and
// X-Content-Type-Options nosniff, or 304 when If-None-Match matches. Other
// methods answer 405. The token never appears in a response.
//
// Raw tokens must never reach logs: hosts pass request paths through
// RedactPath in their logging middleware, and OnError receives the path
// already redacted.
type FeedHandler struct {
	// Resolve maps a token to its Feed. Required.
	Resolve Resolver
	// Options configure the encoding; Options.Domain is required.
	Options Options
	// OnError, when set, receives the redacted request path and every error
	// answered with a 500. A resolver error is passed as returned, so the
	// resolver must not wrap the token into it.
	OnError func(redactedPath string, err error)
}

// ServeHTTP implements http.Handler.
func (h *FeedHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	_, token, _ := splitToken(r.URL.Path)
	if token == "" {
		notFound(w)
		return
	}
	feed, err := h.Resolve(r.Context(), token)
	if denied(err) {
		notFound(w)
		return
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.serveFeed(w, r, feed)
}

// serveFeed encodes feed and answers 200, or 304 when If-None-Match matches.
func (h *FeedHandler) serveFeed(w http.ResponseWriter, r *http.Request, feed Feed) {
	var body bytes.Buffer
	if err := Write(&body, feed, h.Options); err != nil {
		h.fail(w, r, err)
		return
	}
	sum := sha256.Sum256(body.Bytes())
	etag := `"` + hex.EncodeToString(sum[:]) + `"`

	hdr := w.Header()
	hdr.Set("ETag", etag)
	hdr.Set("Cache-Control", feedCacheControl)
	if etagMatches(r.Header.Get("If-None-Match"), etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	hdr.Set("Content-Type", "text/calendar; charset=utf-8")
	hdr.Set("Content-Length", strconv.Itoa(body.Len()))
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodGet {
		_, _ = w.Write(body.Bytes())
	}
}

func (h *FeedHandler) fail(w http.ResponseWriter, r *http.Request, err error) {
	if h.OnError != nil {
		h.OnError(RedactPath(r.URL.Path), err)
	}
	w.Header().Set("Cache-Control", "no-store")
	http.Error(w, "internal error", http.StatusInternalServerError)
}

// denied reports whether a Resolver error means the token grants nothing.
func denied(err error) bool {
	return errors.Is(err, bizkit.ErrNotFound) || errors.Is(err, bizkit.ErrExpired) || errors.Is(err, bizkit.ErrForbidden)
}

// notFound answers the single 404 shared by unknown, expired and forbidden
// tokens, so they are indistinguishable.
func notFound(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	http.Error(w, notFoundBody, http.StatusNotFound)
}

// RedactPath returns p with the feed token FeedHandler would read replaced by
// Redacted, keeping any ".ics" suffix: "/feeds/abc.ics" becomes
// "/feeds/REDACTED.ics". A path with no token is returned unchanged. Hosts
// apply it to feed requests before logging them.
func RedactPath(p string) string {
	prefix, token, suffix := splitToken(p)
	if token == "" {
		return p
	}
	return prefix + Redacted + suffix
}

// splitToken splits p into everything up to the last '/', the token, and
// the ".ics" suffix if present.
func splitToken(p string) (prefix, token, suffix string) {
	i := strings.LastIndexByte(p, '/')
	prefix, token = p[:i+1], p[i+1:]
	if t, ok := strings.CutSuffix(token, icsSuffix); ok {
		return prefix, t, icsSuffix
	}
	return prefix, token, ""
}

// etagMatches applies the weak comparison If-None-Match requires (RFC 9110
// section 13.1.2) to the comma-separated list in header.
func etagMatches(header, etag string) bool {
	for candidate := range strings.SplitSeq(header, ",") {
		candidate = strings.TrimSpace(candidate)
		if candidate == "*" || strings.TrimPrefix(candidate, "W/") == etag {
			return true
		}
	}
	return false
}
