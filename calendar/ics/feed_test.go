package ics

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/arhuman/bizkit"
	"github.com/arhuman/bizkit/calendar"
)

const goodToken = "s3cr3t-good-token"

func resolve(_ context.Context, token string) (Feed, error) {
	switch token {
	case goodToken:
		return Feed{Name: "Team", Events: []calendar.Event{
			{ID: "7", Title: "Holiday", AllDay: true, StartDate: calendar.Date{Year: 2026, Month: 6, Day: 9}},
		}, Updated: time.Date(2026, 1, 1, 8, 0, 0, 0, time.UTC)}, nil
	case "s3cr3t-expired":
		return Feed{}, fmt.Errorf("feed: %w", bizkit.ErrExpired)
	case "s3cr3t-revoked":
		return Feed{}, fmt.Errorf("feed: %w", bizkit.ErrForbidden)
	case "s3cr3t-db-down":
		return Feed{}, errors.New("db down")
	case "s3cr3t-bad-event":
		return Feed{Events: []calendar.Event{{Title: "no id"}}}, nil
	}
	return Feed{}, bizkit.ErrNotFound
}

type logged struct{ paths, errs []string }

func newHandler() (*FeedHandler, *logged) {
	l := &logged{}
	return &FeedHandler{
		Resolve: resolve,
		Options: Options{Domain: "easybiz.example"},
		OnError: func(p string, err error) {
			l.paths = append(l.paths, p)
			l.errs = append(l.errs, err.Error())
		},
	}, l
}

func do(t *testing.T, h http.Handler, method, path string, header http.Header) (*http.Response, string) {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), method, path, nil)
	for k, v := range header {
		req.Header[k] = v
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	res := rec.Result()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return res, string(b)
}

func requireHeader(t *testing.T, res *http.Response, key, want string) {
	t.Helper()
	if got := res.Header.Get(key); got != want {
		t.Errorf("%s = %q, want %q", key, got, want)
	}
}

func TestFeed_GetServesCalendarWithETag(t *testing.T) {
	h, _ := newHandler()
	res, body := do(t, h, http.MethodGet, "/feeds/"+goodToken+".ics", nil)
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
	requireHeader(t, res, "Content-Type", "text/calendar; charset=utf-8")
	requireHeader(t, res, "Cache-Control", "private, no-cache")
	requireHeader(t, res, "X-Content-Type-Options", "nosniff")
	if !strings.HasPrefix(body, "BEGIN:VCALENDAR\r\n") || !strings.Contains(body, "\r\nDTSTART;VALUE=DATE:20260609\r\n") {
		t.Errorf("body is not the feed:\n%s", body)
	}
	sum := sha256.Sum256([]byte(body))
	requireHeader(t, res, "ETag", `"`+hex.EncodeToString(sum[:])+`"`)
	requireHeader(t, res, "Content-Length", fmt.Sprint(len(body)))
}

func TestFeed_PathWithoutSuffixAndMuxRouting(t *testing.T) {
	h, _ := newHandler()
	mux := http.NewServeMux()
	mux.Handle("/feeds/{token}", h)
	res, _ := do(t, mux, http.MethodGet, "/feeds/"+goodToken, nil)
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", res.StatusCode)
	}
}

func TestFeed_IfNoneMatchAnswers304(t *testing.T) {
	h, _ := newHandler()
	path := "/feeds/" + goodToken + ".ics"
	first, _ := do(t, h, http.MethodGet, path, nil)
	defer first.Body.Close()
	etag := first.Header.Get("ETag")
	if etag == "" {
		t.Fatal("no ETag")
	}
	for name, inm := range map[string]string{
		"exact":    etag,
		"weak":     "W/" + etag,
		"in list":  `"other", ` + etag,
		"wildcard": "*",
	} {
		t.Run(name, func(t *testing.T) {
			res, body := do(t, h, http.MethodGet, path, http.Header{"If-None-Match": {inm}})
			defer res.Body.Close()
			if res.StatusCode != http.StatusNotModified {
				t.Fatalf("status = %d, want 304", res.StatusCode)
			}
			if body != "" {
				t.Errorf("304 body = %q, want empty", body)
			}
			requireHeader(t, res, "ETag", etag)
			requireHeader(t, res, "Cache-Control", "private, no-cache")
		})
	}
	res, _ := do(t, h, http.MethodGet, path, http.Header{"If-None-Match": {`"stale"`}})
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Errorf("stale If-None-Match status = %d, want 200", res.StatusCode)
	}
}

func TestFeed_UnknownExpiredForbiddenAreIdentical404(t *testing.T) {
	h, l := newHandler()
	for _, path := range []string{"/feeds/s3cr3t-unknown.ics", "/feeds/s3cr3t-expired.ics", "/feeds/s3cr3t-revoked.ics", "/feeds/", "/feeds/.ics"} {
		t.Run(path, func(t *testing.T) {
			res, body := do(t, h, http.MethodGet, path, nil)
			defer res.Body.Close()
			if res.StatusCode != http.StatusNotFound {
				t.Fatalf("status = %d, want 404", res.StatusCode)
			}
			if body != "not found\n" {
				t.Errorf("body = %q, want %q", body, "not found\n")
			}
			requireHeader(t, res, "Cache-Control", "no-store")
			requireHeader(t, res, "X-Content-Type-Options", "nosniff")
			requireHeader(t, res, "ETag", "")
		})
	}
	if len(l.paths) != 0 {
		t.Errorf("OnError called for 404s: %v", l.paths)
	}
}

func TestFeed_HeadHasHeadersWithoutBody(t *testing.T) {
	h, _ := newHandler()
	get, getBody := do(t, h, http.MethodGet, "/feeds/"+goodToken+".ics", nil)
	defer get.Body.Close()
	res, body := do(t, h, http.MethodHead, "/feeds/"+goodToken+".ics", nil)
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
	if body != "" {
		t.Errorf("HEAD body = %q, want empty", body)
	}
	requireHeader(t, res, "ETag", get.Header.Get("ETag"))
	requireHeader(t, res, "Content-Length", fmt.Sprint(len(getBody)))
	requireHeader(t, res, "Content-Type", "text/calendar; charset=utf-8")
}

func TestFeed_MethodNotAllowed(t *testing.T) {
	h, _ := newHandler()
	for _, m := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		res, _ := do(t, h, m, "/feeds/"+goodToken+".ics", nil)
		defer res.Body.Close()
		if res.StatusCode != http.StatusMethodNotAllowed {
			t.Errorf("%s status = %d, want 405", m, res.StatusCode)
		}
		requireHeader(t, res, "Allow", "GET, HEAD")
	}
}

func TestFeed_RedactionNeverExposesToken(t *testing.T) {
	for in, want := range map[string]string{
		"/feeds/s3cr3t.ics":   "/feeds/REDACTED.ics",
		"/feeds/s3cr3t":       "/feeds/REDACTED",
		"/a/b/s3cr3t.ics":     "/a/b/REDACTED.ics",
		"/feeds/":             "/feeds/",
		"s3cr3t":              "REDACTED",
		"/feeds/.ics":         "/feeds/.ics",
		"/feeds/s3cr3t.ics.x": "/feeds/REDACTED",
	} {
		if got := RedactPath(in); got != want {
			t.Errorf("RedactPath(%q) = %q, want %q", in, got, want)
		}
	}

	h, l := newHandler()
	for _, token := range []string{"s3cr3t-db-down", "s3cr3t-bad-event"} {
		res, body := do(t, h, http.MethodGet, "/feeds/"+token+".ics", nil)
		defer res.Body.Close()
		if res.StatusCode != http.StatusInternalServerError {
			t.Errorf("%s status = %d, want 500", token, res.StatusCode)
		}
		if strings.Contains(body, "s3cr3t") {
			t.Errorf("%s body leaks the token: %q", token, body)
		}
	}
	if want := []string{"/feeds/REDACTED.ics", "/feeds/REDACTED.ics"}; strings.Join(l.paths, "|") != strings.Join(want, "|") {
		t.Errorf("OnError paths = %v, want %v", l.paths, want)
	}
	for _, e := range l.errs {
		if strings.Contains(e, "s3cr3t") {
			t.Errorf("OnError error leaks the token: %q", e)
		}
	}
}

func TestFeed_NilOnErrorStill500(t *testing.T) {
	h := &FeedHandler{Resolve: resolve, Options: Options{Domain: "easybiz.example"}}
	res, _ := do(t, h, http.MethodGet, "/feeds/s3cr3t-db-down.ics", nil)
	defer res.Body.Close()
	if res.StatusCode != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", res.StatusCode)
	}
}
