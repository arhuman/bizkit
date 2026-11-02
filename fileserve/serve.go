// Package fileserve serves stored files over net/http with safe defaults: a
// strict inline allowlist, attachment for everything else, Range support,
// nosniff and an RFC 5987 Content-Disposition filename.
package fileserve

import (
	"io"
	"mime"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/arhuman/bizkit/blob"
)

const (
	defaultContentType = "application/octet-stream"
	defaultFilename    = "download"
	// sandboxCSP blocks script, plugins and subresources should a browser
	// ever render a served file as a document despite its disposition.
	sandboxCSP = "default-src 'none'; style-src 'unsafe-inline'; sandbox"
	pdfType    = "application/pdf"
)

// inlineTypes is the closed set of media types rendered inline. SVG is
// deliberately absent: it is an active document that can carry script.
var inlineTypes = map[string]bool{
	"image/png":  true,
	"image/jpeg": true,
	"image/gif":  true,
	"image/webp": true,
	pdfType:      true,
}

// Meta describes the file Serve writes.
type Meta struct {
	// Name is the filename offered to the client. Any directory part is
	// dropped; an empty name becomes "download".
	Name string
	// ContentType is the declared media type. It is not trusted beyond
	// choosing between the inline allowlist and attachment; an empty or
	// unparsable value is served as application/octet-stream.
	ContentType string
	// ModTime feeds Last-Modified and conditional requests. The zero time
	// disables both.
	ModTime time.Time
}

// Serve writes content to w through http.ServeContent, so Range, HEAD and
// conditional requests behave as in net/http.
//
// The response is inline only when Inline reports true for m.ContentType;
// everything else, SVG included, is an attachment. Every response carries
// X-Content-Type-Options: nosniff and a Content-Disposition with an ASCII
// filename fallback plus an RFC 5987 filename*. Every response except PDF
// also carries a sandboxing Content-Security-Policy; PDF is exempt because
// browser PDF viewers refuse to run in a sandboxed document.
//
// Serve does not close content and does not look up the file: a missing file
// is the caller's to report before calling Serve.
func Serve(w http.ResponseWriter, r *http.Request, m Meta, content io.ReadSeeker) {
	h := w.Header()
	h.Set("Content-Type", normalizeType(m.ContentType))
	SetPolicy(h, m.Name, m.ContentType)
	http.ServeContent(w, r, "", m.ModTime, content)
}

// SetPolicy sets on h the policy headers Serve sends with a file named name
// whose response carries contentType: an inline Content-Disposition when
// Inline reports true and an attachment one otherwise,
// X-Content-Type-Options: nosniff, and the sandboxing Content-Security-Policy
// for every type except PDF.
//
// It is for a handler that writes the response itself, such as a WebDAV
// server, and must run before the status is written. It leaves Content-Type
// and every Range, caching or conditional header untouched, so contentType
// must be the type the response actually declares.
func SetPolicy(h http.Header, name, contentType string) {
	mediaType := normalizeType(contentType)
	disposition := "attachment"
	if inlineTypes[mediaType] {
		disposition = "inline"
	}
	h.Set("Content-Disposition", contentDisposition(disposition, name))
	h.Set("X-Content-Type-Options", "nosniff")
	if mediaType != pdfType {
		h.Set("Content-Security-Policy", sandboxCSP)
	}
}

// ServeObject serves an open blob.Object using its Info for the content type
// and modification time. An empty name falls back to the last segment of the
// object's key. The caller keeps ownership of obj and must Close it.
func ServeObject(w http.ResponseWriter, r *http.Request, name string, obj blob.Object) {
	info := obj.Info()
	if name == "" {
		name = info.Key
	}
	Serve(w, r, Meta{Name: name, ContentType: info.ContentType, ModTime: info.ModTime}, obj)
}

// Inline reports whether contentType is on the inline allowlist: image/png,
// image/jpeg, image/gif, image/webp and application/pdf, matched on the media
// type alone, case-insensitively, ignoring parameters. SVG in any spelling
// and every other type report false.
func Inline(contentType string) bool {
	return inlineTypes[normalizeType(contentType)]
}

// normalizeType returns the lower-cased media type of ct without parameters,
// or application/octet-stream when ct is empty or unparsable.
func normalizeType(ct string) string {
	mediaType, _, err := mime.ParseMediaType(ct)
	if err != nil || !strings.Contains(mediaType, "/") {
		return defaultContentType
	}
	return mediaType
}

// contentDisposition builds the header value with a quoted ASCII fallback and
// a percent-encoded UTF-8 filename* (RFC 6266, RFC 5987).
func contentDisposition(disposition, name string) string {
	name = baseName(strings.ToValidUTF8(name, "_"))
	return disposition + `; filename="` + asciiFallback(name) + `"; filename*=UTF-8''` + encodeExtValue(name)
}

// baseName drops any slash or backslash separated directory part.
func baseName(name string) string {
	if i := strings.LastIndexAny(name, `/\`); i >= 0 {
		name = name[i+1:]
	}
	if strings.TrimSpace(name) == "" {
		return defaultFilename
	}
	return name
}

// asciiFallback replaces every rune that is not printable ASCII, and the
// quote and backslash that would break a quoted-string, with an underscore.
func asciiFallback(name string) string {
	var b strings.Builder
	for _, c := range name {
		if c < 0x20 || c >= utf8.RuneSelf-1 || c == '"' || c == '\\' {
			b.WriteByte('_')
			continue
		}
		b.WriteRune(c)
	}
	return b.String()
}

// encodeExtValue percent-encodes every byte outside the RFC 5987 attr-char set.
func encodeExtValue(name string) string {
	const hex = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(name); i++ {
		c := name[i]
		if isAttrChar(c) {
			b.WriteByte(c)
			continue
		}
		b.WriteByte('%')
		b.WriteByte(hex[c>>4])
		b.WriteByte(hex[c&0x0f])
	}
	return b.String()
}

func isAttrChar(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		return true
	}
	return strings.IndexByte("!#$&+-.^_`|~", c) >= 0
}
