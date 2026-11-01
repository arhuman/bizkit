package fileserve

import (
	"archive/zip"
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/arhuman/bizkit"
)

func checkErr(t *testing.T, content []byte, declared string, allow ...string) error {
	t.Helper()
	_, err := CheckContent(bytes.NewReader(content), declared, allow...)
	return err
}

func TestSniff_RejectsHTMLDeclaredAsPDF(t *testing.T) {
	err := checkErr(t, []byte("<!DOCTYPE html><html><body>x</body></html>"), "application/pdf")
	if !errors.Is(err, bizkit.ErrForbidden) {
		t.Fatalf("err = %v, want ErrForbidden", err)
	}
}

func TestSniff_RejectsActiveDeclaredBenign(t *testing.T) {
	cases := map[string]struct {
		content  string
		declared string
	}{
		"svg as png":              {`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`, "image/png"},
		"xml svg as jpeg":         {`<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg"/>`, "image/jpeg"},
		"xml as pdf":              {`<?xml version="1.0"?><root/>`, "application/pdf"},
		"script as gif":           {"<script>alert(1)</script>", "image/gif"},
		"whitespace html":         {"\r\n\t  <HTML><body>x", "image/webp"},
		"bom svg":                 {"\xEF\xBB\xBF  <SVG/>", "image/png"},
		"utf16le html":            {"\xFF\xFE<\x00h\x00t\x00m\x00l\x00>\x00", "application/pdf"},
		"utf16be svg":             {"\xFE\xFF\x00<\x00s\x00v\x00g\x00>", "application/pdf"},
		"detect html without tag": {"<body onload=alert(1)>", "image/png"},
		"empty declared":          {"<html>", ""},
		"declared not allowed":    {"<svg/>", "text/html"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			err := checkErr(t, []byte(tc.content), tc.declared, "image/svg+xml")
			if !errors.Is(err, bizkit.ErrForbidden) {
				t.Fatalf("err = %v, want ErrForbidden", err)
			}
		})
	}
}

func TestSniff_AllowsExplicitActiveType(t *testing.T) {
	err := checkErr(t, []byte("<svg/>"), "Image/SVG+XML; charset=utf-8", "image/svg+xml")
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
}

func TestSniff_AcceptsDocxSniffedAsZip(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("word/document.xml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(`<?xml version="1.0"?><w:document/>`)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	sniffed, _, err := Sniff(bytes.NewReader(buf.Bytes()))
	if err != nil || sniffed != "application/zip" {
		t.Fatalf("Sniff = %q, %v, want application/zip", sniffed, err)
	}
	docx := "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	if err := checkErr(t, buf.Bytes(), docx); err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
}

func TestSniff_AcceptsBenignContent(t *testing.T) {
	cases := map[string]struct {
		content  []byte
		declared string
	}{
		"binary":     {[]byte{0x00, 0x01, 0xFE, 0x7F, 0x80, 0x00}, "application/octet-stream"},
		"png":        {[]byte("\x89PNG\r\n\x1a\n0000"), "image/png"},
		"pdf":        {[]byte("%PDF-1.7\n"), "application/pdf"},
		"text":       {[]byte("hello <svg> later"), "text/plain"},
		"mismatched": {[]byte("%PDF-1.7\n"), "image/png"},
		"empty":      {nil, "text/plain"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if err := checkErr(t, tc.content, tc.declared); err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
		})
	}
}

func TestSniff_ReplaysPrefixIntact(t *testing.T) {
	content := bytes.Repeat([]byte("0123456789abcdef"), 100)
	sniffed, replay, err := Sniff(iotest.OneByteReader(bytes.NewReader(content)))
	if err != nil {
		t.Fatal(err)
	}
	if sniffed != "text/plain" {
		t.Errorf("sniffed = %q, want text/plain", sniffed)
	}
	got, err := io.ReadAll(replay)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, content) {
		t.Fatalf("replay differs: got %d bytes, want %d", len(got), len(content))
	}

	r, err := CheckContent(strings.NewReader("short"), "text/plain")
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := io.ReadAll(r); string(got) != "short" {
		t.Fatalf("CheckContent replay = %q, want short", got)
	}
}

func TestSniff_PropagatesReadError(t *testing.T) {
	boom := errors.New("boom")
	if _, _, err := Sniff(iotest.ErrReader(boom)); !errors.Is(err, boom) {
		t.Fatalf("Sniff err = %v, want boom", err)
	}
	if _, err := CheckContent(iotest.ErrReader(boom), "text/plain"); !errors.Is(err, boom) {
		t.Fatalf("CheckContent err = %v, want boom", err)
	}
}
