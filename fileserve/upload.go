package fileserve

import (
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"

	"github.com/arhuman/bizkit"
)

// Default limits applied when a Limits field is zero or negative.
const (
	DefaultMaxFileSize  = 32 << 20
	DefaultMaxFiles     = 20
	DefaultMaxTotal     = 128 << 20
	DefaultMaxFieldSize = 64 << 10
	DefaultMaxFields    = 64
)

// defaultBlockedExts are extensions a web server, OS shell or browser may
// execute: server-side scripts, native executables and Windows script hosts.
var defaultBlockedExts = []string{
	"php", "php3", "php4", "php5", "php7", "phtml", "pht", "phar",
	"asp", "aspx", "jsp", "jspx", "cgi", "pl", "py", "rb", "sh", "bash",
	"exe", "dll", "com", "bat", "cmd", "msi", "scr", "ps1", "vbs", "wsf", "hta",
	"jar", "js", "mjs", "htaccess",
}

// Limits bounds one multipart upload. A zero or negative numeric field takes
// its Default value, so the zero Limits is safe.
type Limits struct {
	// MaxFileSize caps the bytes of one file part.
	MaxFileSize int64
	// MaxFiles caps the number of file parts.
	MaxFiles int
	// MaxTotal caps the bytes of all parts, files and fields together. It
	// does not count multipart framing; bound the raw body with
	// http.MaxBytesReader as well.
	MaxTotal int64
	// MaxFieldSize caps the bytes of one non-file field.
	MaxFieldSize int64
	// MaxFields caps the number of non-file fields.
	MaxFields int
	// BlockedExts lists refused filename extensions, without the dot,
	// matched case-insensitively. Nil selects the default list of server
	// scripts and executables; an empty non-nil slice blocks nothing.
	BlockedExts []string
}

// Reader streams the file parts of a multipart request one at a time; it
// never calls ParseMultipartForm and never holds a whole part in memory.
// Non-file fields are read, within MaxFieldSize, into Fields as they pass.
// A Reader is not safe for concurrent use.
type Reader struct {
	mr        *multipart.Reader
	lim       Limits
	blocked   map[string]bool
	fields    url.Values
	files     int
	nfields   int
	totalLeft int64
	cur       *Part
}

// Part is one file part. Its Read enforces MaxFileSize and MaxTotal while
// streaming: the read that would cross a limit fails with an error wrapping
// bizkit.ErrTooLarge, before the excess reaches the caller.
type Part struct {
	// FormName is the form field name.
	FormName string
	// FileName is the client's filename with any directory part dropped.
	FileName string
	// ContentType is the declared media type without parameters,
	// lower-cased; application/octet-stream when absent or unparsable. It is
	// untrusted: pass it with the part to CheckContent.
	ContentType string

	r        io.Reader
	fileLeft int64
	owner    *Reader
}

// NewReader starts streaming r's multipart body under lim. It fails with
// http.ErrNotMultipart, or a similar error, when r is not an unread
// multipart/form-data request.
func NewReader(r *http.Request, lim Limits) (*Reader, error) {
	mr, err := r.MultipartReader()
	if err != nil {
		return nil, fmt.Errorf("fileserve: %w", err)
	}
	lim = lim.withDefaults()
	exts := lim.BlockedExts
	if exts == nil {
		exts = defaultBlockedExts
	}
	blocked := make(map[string]bool, len(exts))
	for _, e := range exts {
		blocked[strings.ToLower(strings.TrimPrefix(e, "."))] = true
	}
	return &Reader{mr: mr, lim: lim, blocked: blocked, fields: url.Values{}, totalLeft: lim.MaxTotal}, nil
}

// Next returns the next file part, or io.EOF when the body is done. It first
// drains whatever the caller left unread of the previous part, through the
// same limits, then reads any non-file fields before the next file into
// Fields. A part with an empty filename is a field.
//
// Errors wrap bizkit.ErrTooLarge for an exceeded size or count, and
// bizkit.ErrForbidden for a filename carrying a blocked extension anywhere in
// its dot-separated suffixes ("x.php.jpg" is refused, as is "x.PHP"). After
// an error the Reader must not be used further.
func (u *Reader) Next() (*Part, error) {
	if u.cur != nil {
		if _, err := io.Copy(io.Discard, u.cur); err != nil {
			return nil, err
		}
		u.cur = nil
	}
	for {
		p, err := u.mr.NextPart()
		if errors.Is(err, io.EOF) {
			return nil, io.EOF
		}
		if err != nil {
			return nil, fmt.Errorf("fileserve: read multipart: %w", err)
		}
		if p.FileName() == "" {
			if err := u.readField(p); err != nil {
				return nil, err
			}
			continue
		}
		return u.openFile(p)
	}
}

// Fields returns the non-file fields read so far. The map is live: it grows
// as Next passes further fields.
func (u *Reader) Fields() url.Values {
	return u.fields
}

func (u *Reader) openFile(p *multipart.Part) (*Part, error) {
	name := p.FileName()
	if u.files >= u.lim.MaxFiles {
		return nil, fmt.Errorf("fileserve: more than %d files: %w", u.lim.MaxFiles, bizkit.ErrTooLarge)
	}
	if ext, ok := u.blockedExt(name); ok {
		return nil, fmt.Errorf("fileserve: file %q has blocked extension %q: %w", name, ext, bizkit.ErrForbidden)
	}
	u.files++
	u.cur = &Part{
		FormName:    p.FormName(),
		FileName:    name,
		ContentType: normalizeType(p.Header.Get("Content-Type")),
		r:           p,
		fileLeft:    u.lim.MaxFileSize,
		owner:       u,
	}
	return u.cur, nil
}

func (u *Reader) readField(p *multipart.Part) error {
	if u.nfields >= u.lim.MaxFields {
		return fmt.Errorf("fileserve: more than %d fields: %w", u.lim.MaxFields, bizkit.ErrTooLarge)
	}
	u.nfields++
	field := &Part{FormName: p.FormName(), r: p, fileLeft: u.lim.MaxFieldSize, owner: u}
	b, err := io.ReadAll(field)
	if err != nil {
		return err
	}
	u.fields.Add(p.FormName(), string(b))
	return nil
}

// blockedExt reports the first blocked extension among every suffix after
// the first dot of name, ignoring trailing dots and spaces that Windows drops.
func (u *Reader) blockedExt(name string) (string, bool) {
	segs := strings.Split(strings.ToLower(strings.TrimRight(name, ". ")), ".")
	for _, s := range segs[1:] {
		if u.blocked[strings.TrimSpace(s)] {
			return s, true
		}
	}
	return "", false
}

// Read reads from the part, failing with an error wrapping bizkit.ErrTooLarge
// once the part holds more than MaxFileSize bytes or the upload more than
// MaxTotal.
func (p *Part) Read(b []byte) (int, error) {
	allow := min(p.fileLeft, p.owner.totalLeft)
	if allow <= 0 {
		return p.probe()
	}
	if int64(len(b)) > allow {
		b = b[:allow]
	}
	n, err := p.r.Read(b)
	p.fileLeft -= int64(n)
	p.owner.totalLeft -= int64(n)
	return n, err
}

// probe reads one byte once a limit is spent: io.EOF means the part fit
// exactly, anything else means it is over.
func (p *Part) probe() (int, error) {
	var one [1]byte
	for {
		n, err := p.r.Read(one[:])
		if n > 0 {
			break
		}
		if err != nil {
			return 0, err
		}
	}
	if p.fileLeft <= 0 {
		return 0, fmt.Errorf("fileserve: part %q over %d bytes: %w", p.FormName, p.owner.lim.size(p), bizkit.ErrTooLarge)
	}
	return 0, fmt.Errorf("fileserve: upload over %d bytes: %w", p.owner.lim.MaxTotal, bizkit.ErrTooLarge)
}

// size is the per-part limit that applies to p.
func (l Limits) size(p *Part) int64 {
	if p.FileName == "" {
		return l.MaxFieldSize
	}
	return l.MaxFileSize
}

func (l Limits) withDefaults() Limits {
	if l.MaxFileSize <= 0 {
		l.MaxFileSize = DefaultMaxFileSize
	}
	if l.MaxFiles <= 0 {
		l.MaxFiles = DefaultMaxFiles
	}
	if l.MaxTotal <= 0 {
		l.MaxTotal = DefaultMaxTotal
	}
	if l.MaxFieldSize <= 0 {
		l.MaxFieldSize = DefaultMaxFieldSize
	}
	if l.MaxFields <= 0 {
		l.MaxFields = DefaultMaxFields
	}
	return l
}
