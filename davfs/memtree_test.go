package davfs_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"maps"
	"path"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/arhuman/bizkit"
	"github.com/arhuman/bizkit/davfs"
)

// memTree is an in-memory davfs.Tree for tests. Its Put honours the Tree
// contract: it reads the whole body first and stores nothing when a read fails.
type memTree struct {
	mu    sync.Mutex
	nodes map[string]*memNode
	// deny makes every operation on these names fail with ErrForbidden.
	deny map[string]bool
	// quota makes a Put longer than this many bytes fail with ErrTooLarge.
	quota int
	calls int
	lists int
	opens int
	stats map[string]int
}

type memNode struct {
	dir  bool
	data []byte
}

var epoch = time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)

// newMemTree builds a tree from paths; a trailing slash marks a directory,
// anything else is a file holding its own value.
func newMemTree(files map[string]string) *memTree {
	t := &memTree{nodes: map[string]*memNode{}, deny: map[string]bool{}, stats: map[string]int{}}
	for p, content := range files {
		if strings.HasSuffix(p, "/") {
			t.nodes[strings.TrimSuffix(p, "/")] = &memNode{dir: true}
			continue
		}
		t.nodes[p] = &memNode{data: []byte(content)}
	}
	return t
}

func (t *memTree) check(name string) error {
	t.calls++
	if t.deny[name] {
		return fmt.Errorf("mem %q: %w", name, bizkit.ErrForbidden)
	}
	return nil
}

func (t *memTree) parentExists(name string) bool {
	parent := path.Dir(name)
	if parent == "/" {
		return true
	}
	n, ok := t.nodes[parent]
	return ok && n.dir
}

func (t *memTree) Stat(_ context.Context, name string) (davfs.Entry, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.stats[name]++
	if err := t.check(name); err != nil {
		return davfs.Entry{}, err
	}
	n, ok := t.nodes[name]
	if !ok {
		return davfs.Entry{}, fmt.Errorf("mem %q: %w", name, bizkit.ErrNotFound)
	}
	return davfs.Entry{Name: path.Base(name), Size: int64(len(n.data)), ModTime: epoch, IsDir: n.dir}, nil
}

func (t *memTree) List(_ context.Context, dir string) ([]davfs.Entry, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.lists++
	if err := t.check(dir); err != nil {
		return nil, err
	}
	var out []davfs.Entry
	for p, n := range t.nodes {
		if path.Dir(p) == dir {
			out = append(out, davfs.Entry{Name: path.Base(p), Size: int64(len(n.data)), ModTime: epoch, IsDir: n.dir})
		}
	}
	slices.SortFunc(out, func(a, b davfs.Entry) int { return strings.Compare(a.Name, b.Name) })
	return out, nil
}

type memReader struct{ *bytes.Reader }

func (memReader) Close() error { return nil }

func (t *memTree) Open(_ context.Context, name string) (io.ReadSeekCloser, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.opens++
	if err := t.check(name); err != nil {
		return nil, err
	}
	n, ok := t.nodes[name]
	if !ok || n.dir {
		return nil, fmt.Errorf("mem %q: %w", name, bizkit.ErrNotFound)
	}
	return memReader{bytes.NewReader(n.data)}, nil
}

func (t *memTree) Put(_ context.Context, name string, r io.Reader) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if err := t.check(name); err != nil {
		return err
	}
	if t.quota > 0 && len(data) > t.quota {
		return fmt.Errorf("mem %q: %w", name, bizkit.ErrTooLarge)
	}
	t.nodes[name] = &memNode{data: data}
	return nil
}

func (t *memTree) Mkdir(_ context.Context, name string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if err := t.check(name); err != nil {
		return err
	}
	if !t.parentExists(name) {
		return fmt.Errorf("mem %q: parent: %w", name, bizkit.ErrNotFound)
	}
	if _, ok := t.nodes[name]; ok {
		return fmt.Errorf("mem %q: exists", name)
	}
	t.nodes[name] = &memNode{dir: true}
	return nil
}

func (t *memTree) Remove(_ context.Context, name string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if err := t.check(name); err != nil {
		return err
	}
	if _, ok := t.nodes[name]; !ok {
		return fmt.Errorf("mem %q: %w", name, bizkit.ErrNotFound)
	}
	for p := range t.nodes {
		if p == name || strings.HasPrefix(p, name+"/") {
			delete(t.nodes, p)
		}
	}
	return nil
}

func (t *memTree) Rename(_ context.Context, oldName, newName string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if err := t.check(oldName); err != nil {
		return err
	}
	if _, ok := t.nodes[oldName]; !ok {
		return fmt.Errorf("mem %q: %w", oldName, bizkit.ErrNotFound)
	}
	if !t.parentExists(newName) {
		return fmt.Errorf("mem %q: parent: %w", newName, bizkit.ErrNotFound)
	}
	moved := map[string]*memNode{}
	for p, n := range t.nodes {
		if p == oldName || strings.HasPrefix(p, oldName+"/") {
			moved[newName+strings.TrimPrefix(p, oldName)] = n
			delete(t.nodes, p)
		}
	}
	for p, n := range moved {
		t.nodes[p] = n
	}
	return nil
}

// snapshot renders the tree as sorted "path" (directory) and
// "path=content" (file) lines, for literal comparisons.
func (t *memTree) snapshot() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]string, 0, len(t.nodes))
	for p, n := range t.nodes {
		if n.dir {
			out = append(out, p+"/")
			continue
		}
		out = append(out, p+"="+string(n.data))
	}
	slices.Sort(out)
	return out
}

func (t *memTree) callCount() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.calls
}

// listOpenCounts reports how many times List and Open were called.
func (t *memTree) listOpenCounts() (lists, opens int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.lists, t.opens
}

// statCounts reports how many times Stat was called, per name.
func (t *memTree) statCounts() map[string]int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return maps.Clone(t.stats)
}
