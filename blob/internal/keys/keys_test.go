package keys

import "testing"

func TestClean(t *testing.T) {
	for in, want := range map[string]string{
		"a/b.txt":          "a/b.txt",
		"/a//b.txt":        "a/b.txt",
		"a/./c/../b.txt":   "a/b.txt",
		"../../etc/passwd": "etc/passwd",
		`dir\file`:         "dir/file",
		"a/":               "a",
		"/":                "",
		"":                 "",
	} {
		if got := Clean(in); got != want {
			t.Errorf("Clean(%q) = %q, want %q", in, got, want)
		}
	}
}
