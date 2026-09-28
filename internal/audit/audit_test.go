// Package audit holds repository-wide security invariants.
package audit

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Filedeck never executes programs or loads code: File Browser's command,
// hook and shell advisories (GHSA-8c9q, -jvpw, -m93h, -3q2w, -hc8f, -w7qc,
// -39cx) have no attack surface here. This test keeps it that way.
func TestNoProcessExecutionOrPlugins(t *testing.T) {
	forbidden := map[string]bool{"os/exec": true, "plugin": true, "net/http/cgi": true, "net/http/fcgi": true, "text/template": true, "html/template": true}
	root := filepath.Join("..", "..")
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == "node_modules" || d.Name() == ".git" || d.Name() == "test") {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(token.NewFileSet(), p, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, imp := range f.Imports {
			path, _ := strconv.Unquote(imp.Path.Value)
			if forbidden[path] {
				t.Errorf("%s imports %s", p, path)
			}
		}
		src, _ := os.ReadFile(p)
		for _, call := range []string{"syscall.Exec(", "unix.Exec(", "syscall.ForkExec(", "unix.ForkExec("} {
			if strings.Contains(string(src), call) {
				t.Errorf("%s calls %s", p, call)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
