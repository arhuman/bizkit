// Package archtest enforces the module's dependency boundaries: bizkit is
// net/http-native and storage-agnostic, so framework and driver imports are
// confined to the adapter packages named below.
package archtest

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"testing"
)

const module = "github.com/arhuman/bizkit"

type rule struct {
	forbidden string
	// exact matches forbidden alone, not its subpackages.
	exact bool
	// allowedIn lists the module-relative package trees that may import forbidden.
	allowedIn []string
}

var rules = []rule{
	{forbidden: "github.com/gin-gonic/gin", allowedIn: []string{"ginx"}},
	{forbidden: "github.com/minio/minio-go", allowedIn: []string{"blob/s3"}},
	// database/sql/driver holds interfaces only; google/uuid and rs/xid import it.
	{forbidden: "database/sql", exact: true},
	{forbidden: "github.com/jackc/pgx"},
	{forbidden: "github.com/lib/pq"},
	{forbidden: "github.com/mattn/go-sqlite3"},
	{forbidden: "modernc.org/sqlite"},
	{forbidden: "github.com/go-sql-driver/mysql"},
	{forbidden: "gorm.io"},
}

type pkg struct {
	ImportPath   string
	Deps         []string
	TestImports  []string
	XTestImports []string
	Error        *struct{ Err string }
}

func TestNoDriverOrGinOutsideAdapters(t *testing.T) {
	pkgs := listModulePackages(t)

	sawRoot := false
	for _, p := range pkgs {
		if p.ImportPath == module {
			sawRoot = true
		}
		rel := strings.TrimPrefix(strings.TrimPrefix(p.ImportPath, module), "/")
		// Deps is transitive, so a package importing ginx is caught too.
		imports := append(append(append([]string{}, p.Deps...), p.TestImports...), p.XTestImports...)
		for _, v := range violations(rel, imports) {
			t.Errorf("%s %s", p.ImportPath, v)
		}
	}
	if !sawRoot {
		t.Fatalf("go list did not report %s; the check ran on nothing", module)
	}
}

func TestSQLDriverInterfaceAllowedTransitively(t *testing.T) {
	if got := violations("blob", []string{"database/sql/driver"}); len(got) != 0 {
		t.Errorf("database/sql/driver rejected: %v", got)
	}
	for _, imp := range []string{"database/sql", "github.com/jackc/pgx/v5"} {
		if got := violations("blob", []string{imp}); len(got) != 1 {
			t.Errorf("%s: got %d violations, want 1", imp, len(got))
		}
	}
}

// violations reports each import that a rule forbids for the package at the
// module-relative path rel.
func violations(rel string, imports []string) []string {
	var out []string
	for _, r := range rules {
		if within(rel, r.allowedIn) {
			continue
		}
		for _, imp := range imports {
			if matches(imp, r) {
				out = append(out, fmt.Sprintf("imports %s; allowed only in %v", imp, r.allowedIn))
			}
		}
	}
	return out
}

func listModulePackages(t *testing.T) []pkg {
	t.Helper()
	cmd := exec.Command("go", "list", "-e", "-json=ImportPath,Deps,TestImports,XTestImports,Error", module+"/...")
	cmd.Dir = "../.."
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}
	var pkgs []pkg
	dec := json.NewDecoder(strings.NewReader(string(out)))
	for {
		var p pkg
		if err := dec.Decode(&p); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			t.Fatalf("decode go list output: %v", err)
		}
		if p.Error != nil {
			t.Fatalf("go list %s: %s", p.ImportPath, p.Error.Err)
		}
		pkgs = append(pkgs, p)
	}
	return pkgs
}

func matches(imp string, r rule) bool {
	if r.exact {
		return imp == r.forbidden
	}
	return imp == r.forbidden || strings.HasPrefix(imp, r.forbidden+"/")
}

func within(rel string, trees []string) bool {
	for _, tree := range trees {
		if rel == tree || strings.HasPrefix(rel, tree+"/") {
			return true
		}
	}
	return false
}
