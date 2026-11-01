package fileserve

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"

	"github.com/arhuman/bizkit"
)

// sniffLen is the prefix http.DetectContentType considers.
const sniffLen = 512

// activeTypes are the sniffed types a browser may run as a document or script.
var activeTypes = map[string]bool{
	"text/html":             true,
	"text/xml":              true,
	"application/xml":       true,
	"application/xhtml+xml": true,
	"image/svg+xml":         true,
}

// activeMarkers are the lower-cased leading tokens that make a prefix active,
// checked after any byte order mark and leading whitespace. Order matters:
// "<?xml" is refined to SVG when the prefix also holds "<svg".
var activeMarkers = []struct {
	prefix    string
	mediaType string
}{
	{"<svg", "image/svg+xml"},
	{"<?xml", "text/xml"},
	{"<!doctype html", "text/html"},
	{"<html", "text/html"},
	{"<script", "text/html"},
}

// Sniff reads up to the first 512 bytes of r and returns the detected media
// type, without parameters, and a reader that replays those bytes followed
// by the rest of r, so the caller sees the stream intact.
//
// Detection is http.DetectContentType, refined for active content it misses:
// after an optional UTF-8 or UTF-16 byte order mark and leading whitespace, a
// case-insensitive "<svg" reports image/svg+xml, "<?xml" reports text/xml (or
// image/svg+xml when the prefix also contains "<svg"), and "<!doctype html",
// "<html" or "<script" report text/html. Sniff never closes r. A read error
// other than a short stream is returned with a nil reader.
func Sniff(r io.Reader) (string, io.Reader, error) {
	head := make([]byte, sniffLen)
	n, err := io.ReadFull(r, head)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return "", nil, fmt.Errorf("fileserve: sniff: %w", err)
	}
	head = head[:n]
	return sniffType(head), io.MultiReader(bytes.NewReader(head), r), nil
}

// CheckContent sniffs r and rejects active content (HTML, XML, SVG, script)
// unless the declared type is one of allow, matched on the media type alone,
// case-insensitively. It never requires the declared and sniffed types to
// agree: a docx declared as such sniffs as application/zip and passes, as
// does any non-active content under any declared type. An empty or
// unparsable declared type counts as application/octet-stream.
//
// On success it returns a reader replaying the whole stream; a rejection
// wraps bizkit.ErrForbidden. Pass allow only for active types the caller
// means to accept, such as "image/svg+xml" for an SVG upload.
func CheckContent(r io.Reader, declared string, allow ...string) (io.Reader, error) {
	sniffed, replay, err := Sniff(r)
	if err != nil {
		return nil, err
	}
	if !activeTypes[sniffed] {
		return replay, nil
	}
	declared = normalizeType(declared)
	if slices.ContainsFunc(allow, func(a string) bool { return normalizeType(a) == declared }) {
		return replay, nil
	}
	return nil, fmt.Errorf("fileserve: %s content declared as %s: %w", sniffed, declared, bizkit.ErrForbidden)
}

// sniffType classifies head, preferring the active-content markers over
// http.DetectContentType.
func sniffType(head []byte) string {
	text := bytes.ToLower(bytes.TrimLeft(stripBOM(head), " \t\r\n\f"))
	for _, m := range activeMarkers {
		if !bytes.HasPrefix(text, []byte(m.prefix)) {
			continue
		}
		if m.mediaType == "text/xml" && bytes.Contains(text, []byte("<svg")) {
			return "image/svg+xml"
		}
		return m.mediaType
	}
	return normalizeType(http.DetectContentType(head))
}

// stripBOM drops a UTF-8 byte order mark, and narrows UTF-16 text to its low
// bytes so the ASCII markers match; non-ASCII runes become noise, which is
// harmless since every marker is ASCII.
func stripBOM(b []byte) []byte {
	switch {
	case bytes.HasPrefix(b, []byte{0xEF, 0xBB, 0xBF}):
		return b[3:]
	case bytes.HasPrefix(b, []byte{0xFE, 0xFF}):
		return everyOther(b[2:], 1)
	case bytes.HasPrefix(b, []byte{0xFF, 0xFE}):
		return everyOther(b[2:], 0)
	}
	return b
}

// everyOther keeps b[from], b[from+2], b[from+4] and so on.
func everyOther(b []byte, from int) []byte {
	out := make([]byte, 0, len(b)/2+1)
	for i := from; i < len(b); i += 2 {
		out = append(out, b[i])
	}
	return out
}
