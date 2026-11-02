// Package blobtest is the conformance suite every blob.Backend must pass.
//
// A backend's test calls Run with a factory returning a fresh, empty backend;
// each case runs as a subtest on its own backend, so a failure names the
// broken behavior and cases never see each other's objects.
package blobtest

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/arhuman/bizkit"
	"github.com/arhuman/bizkit/blob"
)

// Factory returns a fresh, empty backend for one subtest. It registers any
// teardown with t.Cleanup.
type Factory func(t *testing.T) blob.Backend

// Run executes the whole conformance suite against backends from newBackend.
func Run(t *testing.T, newBackend Factory) {
	t.Helper()
	cases := []struct {
		name string
		fn   func(*testing.T, blob.Backend)
	}{
		{"RoundTrip", testRoundTrip},
		{"MissingKey", testMissingKey},
		{"EmptyObject", testEmptyObject},
		{"SeekProbe", testSeekProbe},
		{"RangeRead", testRangeRead},
		{"MaxSize", testMaxSize},
		{"Overwrite", testOverwrite},
		{"List", testList},
		{"ListPaged", testListPaged},
		{"KeyAndPrefix", testKeyAndPrefix},
		{"KeyNormalization", testKeyNormalization},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			c.fn(t, newBackend(t))
		})
	}
}

const sample = "0123456789abcdefghij"

// check records a failure and lets the case continue when ok is false.
func check(t *testing.T, ok bool, format string, args ...any) {
	t.Helper()
	if !ok {
		t.Errorf(format, args...)
	}
}

// require stops the case when ok is false.
func require(t *testing.T, ok bool, format string, args ...any) {
	t.Helper()
	if !ok {
		t.Fatalf(format, args...)
	}
}

func put(t *testing.T, b blob.Backend, key, content string) blob.Info {
	t.Helper()
	info, err := b.Put(context.Background(), key, strings.NewReader(content), blob.PutOptions{})
	require(t, err == nil, "Put(%q): %v", key, err)
	return info
}

func open(t *testing.T, b blob.Backend, key string) blob.Object {
	t.Helper()
	obj, err := b.Open(context.Background(), key)
	require(t, err == nil, "Open(%q): %v", key, err)
	t.Cleanup(func() { _ = obj.Close() })
	return obj
}

func readAll(t *testing.T, r io.Reader) string {
	t.Helper()
	got, err := io.ReadAll(r)
	require(t, err == nil, "read: %v", err)
	return string(got)
}

func testRoundTrip(t *testing.T, b blob.Backend) {
	ctx := context.Background()
	key := "docs/report.txt"
	size := int64(len(sample))
	info, err := b.Put(ctx, key, strings.NewReader(sample), blob.PutOptions{ContentType: "text/plain"})
	require(t, err == nil, "Put: %v", err)
	check(t, info.Key == key && info.Size == size,
		"Put info = {Key:%q Size:%d}, want {Key:%q Size:%d}", info.Key, info.Size, key, size)

	st, err := b.Stat(ctx, key)
	require(t, err == nil, "Stat: %v", err)
	check(t, st.Key == key && st.Size == size,
		"Stat = {Key:%q Size:%d}, want {Key:%q Size:%d}", st.Key, st.Size, key, size)

	obj := open(t, b, key)
	got := obj.Info()
	check(t, got.Key == key && got.Size == size,
		"Open Info = {Key:%q Size:%d}, want {Key:%q Size:%d}", got.Key, got.Size, key, size)
	content := readAll(t, obj)
	check(t, content == sample, "content = %q, want %q", content, sample)

	err = b.Delete(ctx, key)
	require(t, err == nil, "Delete: %v", err)
	_, err = b.Stat(ctx, key)
	check(t, errors.Is(err, bizkit.ErrNotFound), "Stat after Delete err = %v, want ErrNotFound", err)
}

func testMissingKey(t *testing.T, b blob.Backend) {
	ctx := context.Background()
	obj, err := b.Open(ctx, "missing/key.bin")
	if obj != nil {
		_ = obj.Close()
	}
	check(t, errors.Is(err, bizkit.ErrNotFound), "Open missing err = %v, want ErrNotFound at open", err)
	_, err = b.Stat(ctx, "missing/key.bin")
	check(t, errors.Is(err, bizkit.ErrNotFound), "Stat missing err = %v, want ErrNotFound", err)
	err = b.Delete(ctx, "missing/key.bin")
	check(t, err == nil, "Delete missing err = %v, want nil", err)
}

func testEmptyObject(t *testing.T, b blob.Backend) {
	key := "empty.bin"
	info := put(t, b, key, "")
	check(t, info.Size == 0, "Put size = %d, want 0", info.Size)
	st, err := b.Stat(context.Background(), key)
	require(t, err == nil, "Stat: %v", err)
	check(t, st.Size == 0, "Stat size = %d, want 0", st.Size)
	obj := open(t, b, key)
	check(t, obj.Info().Size == 0, "Open Info size = %d, want 0", obj.Info().Size)
	got := readAll(t, obj)
	check(t, got == "", "content = %q, want empty", got)
}

// testSeekProbe mirrors how http.ServeContent sizes a body: seek to the end,
// then back to the start, then read everything.
func testSeekProbe(t *testing.T, b blob.Backend) {
	key := "probe.bin"
	put(t, b, key, sample)
	obj := open(t, b, key)

	end, err := obj.Seek(0, io.SeekEnd)
	require(t, err == nil, "Seek(End): %v", err)
	check(t, end == int64(len(sample)), "Seek(End) = %d, want %d", end, len(sample))
	start, err := obj.Seek(0, io.SeekStart)
	require(t, err == nil, "Seek(Start): %v", err)
	check(t, start == 0, "Seek(Start) = %d, want 0", start)
	got := readAll(t, obj)
	check(t, got == sample, "content after probe = %q, want %q", got, sample)
}

func testRangeRead(t *testing.T, b blob.Backend) {
	key := "range.bin"
	put(t, b, key, sample)
	obj := open(t, b, key)

	const off, n = 5, 7
	pos, err := obj.Seek(off, io.SeekStart)
	require(t, err == nil && pos == off, "Seek(%d) = %d, %v", off, pos, err)
	buf := make([]byte, n)
	_, err = io.ReadFull(obj, buf)
	require(t, err == nil, "ReadFull: %v", err)
	want := sample[off : off+n]
	check(t, string(buf) == want, "range [%d,%d) = %q, want %q", off, off+n, buf, want)

	pos, err = obj.Seek(-3, io.SeekEnd)
	require(t, err == nil && pos == int64(len(sample)-3), "Seek(-3, End) = %d, %v", pos, err)
	got, want := readAll(t, obj), sample[len(sample)-3:]
	check(t, got == want, "suffix = %q, want %q", got, want)
}

func testMaxSize(t *testing.T, b blob.Backend) {
	ctx := context.Background()
	limit := int64(len(sample))

	_, err := b.Put(ctx, "exact.bin", strings.NewReader(sample), blob.PutOptions{MaxSize: limit})
	check(t, err == nil, "Put of exactly MaxSize bytes: %v", err)

	over := bytes.NewReader([]byte(sample + "x"))
	_, err = b.Put(ctx, "over.bin", over, blob.PutOptions{MaxSize: limit})
	require(t, errors.Is(err, bizkit.ErrTooLarge), "Put over MaxSize err = %v, want ErrTooLarge", err)
	_, err = b.Stat(ctx, "over.bin")
	check(t, errors.Is(err, bizkit.ErrNotFound), "Stat after rejected Put err = %v, want ErrNotFound (no partial object)", err)
}

func testOverwrite(t *testing.T, b blob.Backend) {
	key := "same.txt"
	put(t, b, key, sample)
	put(t, b, key, "short")
	got := readAll(t, open(t, b, key))
	check(t, got == "short", "content after overwrite = %q, want %q", got, "short")
}

func testList(t *testing.T, b blob.Backend) {
	for _, k := range []string{"ws1/a.txt", "ws1/sub/b.txt", "ws10/c.txt", "ws2/d.txt"} {
		put(t, b, k, k)
	}
	ctx := context.Background()
	for _, tc := range []struct {
		prefix string
		want   []string
	}{
		{"ws1/", []string{"ws1/a.txt", "ws1/sub/b.txt"}},
		{"ws1", []string{"ws1/a.txt", "ws1/sub/b.txt", "ws10/c.txt"}},
		{"", []string{"ws1/a.txt", "ws1/sub/b.txt", "ws10/c.txt", "ws2/d.txt"}},
		{"nope/", nil},
	} {
		got, err := b.List(ctx, tc.prefix)
		require(t, err == nil, "List(%q): %v", tc.prefix, err)
		keys := make([]string, 0, len(got))
		for _, info := range got {
			keys = append(keys, info.Key)
			check(t, info.Size == int64(len(info.Key)),
				"List(%q) %s size = %d, want %d", tc.prefix, info.Key, info.Size, len(info.Key))
		}
		check(t, strings.Join(keys, ",") == strings.Join(tc.want, ","), "List(%q) = %v, want %v", tc.prefix, keys, tc.want)
	}
}

// testListPaged pages through a prefix and checks the pages join into List's
// result. The keys "p/a-b", "p/a/x" and "p/a0" sort in byte order, which a
// directory walk does not visit in.
func testListPaged(t *testing.T, b blob.Backend) {
	keys := []string{"p/a/y", "p/b", "p/a0", "p/a-b", "q/z", "p/a/x"}
	for _, k := range keys {
		put(t, b, k, k)
	}
	ctx := context.Background()
	all, err := b.List(ctx, "p/")
	require(t, err == nil, "List: %v", err)
	check(t, strings.Join(infoKeys(all), ",") == "p/a-b,p/a/x,p/a/y,p/a0,p/b",
		"List(%q) = %v, want byte order", "p/", infoKeys(all))

	for _, tc := range []struct {
		prefix, after string
		limit         int
		want          []string
	}{
		{"p/", "", 2, []string{"p/a-b,p/a/x", "p/a/y,p/a0", "p/b"}},
		{"p/", "", 5, []string{"p/a-b,p/a/x,p/a/y,p/a0,p/b"}},
		{"p/", "", 0, []string{"p/a-b,p/a/x,p/a/y,p/a0,p/b"}},
		{"p/a", "", 3, []string{"p/a-b,p/a/x,p/a/y", "p/a0"}},
		{"p/", "p/a/", 2, []string{"p/a/x,p/a/y", "p/a0,p/b"}},
		{"p/", "p/a/y", 10, []string{"p/a0,p/b"}},
		{"p/", "p/b", 1, []string{""}},
		{"", "p/b", 0, []string{"q/z"}},
	} {
		after := tc.after
		var pages []string
		for range len(keys) + 1 {
			page, next, err := b.ListPage(ctx, tc.prefix, after, tc.limit)
			require(t, err == nil, "ListPage(%q, %q, %d): %v", tc.prefix, after, tc.limit, err)
			got := infoKeys(page)
			pages = append(pages, strings.Join(got, ","))
			if next == "" {
				break
			}
			check(t, len(got) > 0 && next == got[len(got)-1],
				"ListPage(%q, %q, %d) cursor = %q, want the last key of %v", tc.prefix, after, tc.limit, next, got)
			after = next
		}
		check(t, strings.Join(pages, "|") == strings.Join(tc.want, "|"),
			"ListPage(%q, after %q, limit %d) pages = %q, want %q", tc.prefix, tc.after, tc.limit, pages, tc.want)
	}
}

func infoKeys(infos []blob.Info) []string {
	keys := make([]string, 0, len(infos))
	for _, info := range infos {
		keys = append(keys, info.Key)
	}
	return keys
}

// testKeyAndPrefix covers a key that is also a prefix of another key. The
// backend may store both or refuse the second Put with bizkit.ErrForbidden;
// either way the first object survives, and deleting a key that is only a
// prefix is a no-op.
func testKeyAndPrefix(t *testing.T, b blob.Backend) {
	ctx := context.Background()
	for _, tc := range []struct{ first, second string }{
		{"a/x", "a"},
		{"b", "b/y"},
	} {
		put(t, b, tc.first, tc.first)
		_, err := b.Put(ctx, tc.second, strings.NewReader(tc.second), blob.PutOptions{})
		require(t, err == nil || errors.Is(err, bizkit.ErrForbidden),
			"Put(%q) after Put(%q) err = %v, want nil or ErrForbidden", tc.second, tc.first, err)
		if err == nil {
			got := readAll(t, open(t, b, tc.second))
			check(t, got == tc.second, "content of %q = %q, want %q", tc.second, got, tc.second)
		} else {
			_, err = b.Stat(ctx, tc.second)
			check(t, errors.Is(err, bizkit.ErrNotFound), "Stat(%q) after refused Put err = %v, want ErrNotFound", tc.second, err)
		}
		got := readAll(t, open(t, b, tc.first))
		check(t, got == tc.first, "content of %q = %q, want %q", tc.first, got, tc.first)
	}

	put(t, b, "p/x", "p/x")
	err := b.Delete(ctx, "p")
	check(t, err == nil, "Delete of prefix-only key %q err = %v, want nil", "p", err)
	got := readAll(t, open(t, b, "p/x"))
	check(t, got == "p/x", "content of %q after Delete(%q) = %q, want %q", "p/x", "p", got, "p/x")
}

// testKeyNormalization checks every backend stores a key under the same
// logical name: backslashes become slashes, doubled slashes and dot segments
// collapse, a leading slash or ".." above the root is dropped.
func testKeyNormalization(t *testing.T, b blob.Backend) {
	ctx := context.Background()
	for _, tc := range []struct{ in, want string }{
		{`n\a.txt`, "n/a.txt"},
		{"n//b.txt", "n/b.txt"},
		{"n/./x/../c.txt", "n/c.txt"},
		{"/n/d.txt", "n/d.txt"},
		{"../n/e.txt", "n/e.txt"},
	} {
		info := put(t, b, tc.in, tc.want)
		check(t, info.Key == tc.want, "Put(%q) Key = %q, want %q", tc.in, info.Key, tc.want)
		st, err := b.Stat(ctx, tc.want)
		require(t, err == nil, "Stat(%q) after Put(%q): %v", tc.want, tc.in, err)
		check(t, st.Key == tc.want, "Stat(%q) Key = %q, want %q", tc.want, st.Key, tc.want)
		got := readAll(t, open(t, b, tc.in))
		check(t, got == tc.want, "Open(%q) content = %q, want %q", tc.in, got, tc.want)
	}
	all, err := b.List(ctx, "n/")
	require(t, err == nil, "List: %v", err)
	want := "n/a.txt,n/b.txt,n/c.txt,n/d.txt,n/e.txt"
	check(t, strings.Join(infoKeys(all), ",") == want, "List(%q) = %v, want %s", "n/", infoKeys(all), want)
}
