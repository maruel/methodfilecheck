// Copyright 2026 Marc-Antoine Ruel. All Rights Reserved. Use of this
// source code is governed by the Apache v2 license that can be found in the
// LICENSE file.

package methodfilecheck

import (
	"bytes"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/golangci/plugin-module-register/register"
	"golang.org/x/tools/go/analysis"
)

func TestRegistered(t *testing.T) {
	newPlugin, err := register.GetPlugin("methodfilecheck")
	if err != nil {
		t.Fatal(err)
	}
	p, err := newPlugin(map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if p.GetLoadMode() != register.LoadModeSyntax {
		t.Fatalf("got %q", p.GetLoadMode())
	}
	analyzers, err := p.BuildAnalyzers()
	if err != nil {
		t.Fatal(err)
	}
	if len(analyzers) != 1 || analyzers[0].Name != "methodfilecheck" {
		t.Fatalf("got %+v", analyzers)
	}
	if _, err := newPlugin(map[string]any{"nope": 1}); err == nil {
		t.Fatal("expected an error on unknown settings")
	}
}

// applyEdits applies the edits to the source, like golangci-lint's fixer would.
func applyEdits(t *testing.T, fset *token.FileSet, src []byte, edits []analysis.TextEdit) []byte {
	t.Helper()
	type rng struct {
		start, end int
		text       []byte
	}
	rs := make([]rng, len(edits))
	for i, e := range edits {
		rs[i] = rng{fset.Position(e.Pos).Offset, fset.Position(e.End).Offset, e.NewText}
	}
	slices.SortFunc(rs, func(a, b rng) int { return a.start - b.start })
	var out []byte
	prev := 0
	for _, r := range rs {
		out = append(out, src[prev:r.start]...)
		out = append(out, r.text...)
		prev = r.end
	}
	return append(out, src[prev:]...)
}

// write writes a source file to disk.
func write(t *testing.T, path, src string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestSuggestedEdits(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	src := "package p\n\ntype Foo struct{}\n\nfunc helper() {}\n\nfunc (f *Foo) A() {}\n"
	path := filepath.Join(dir, "a.go")
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, src, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	vs := CheckSyntax(fset, []*ast.File{f})
	if len(vs) != 1 || vs[0].Fix == nil {
		t.Fatalf("expected one violation with a fix, got %+v", vs)
	}
	edits, ok := suggestedEdits(fset, vs[0])
	if !ok || len(edits) != 1 {
		t.Fatalf("expected one edit, got %v, %v", edits, ok)
	}
	// Edits must be sorted and non-overlapping.
	last := -1
	for _, e := range edits {
		start := fset.Position(e.Pos).Offset
		if start < last {
			t.Fatalf("edits are not sorted: %d after %d", start, last)
		}
		last = fset.Position(e.End).Offset
	}
	fixed := applyEdits(t, fset, []byte(src), edits)
	// golangci-lint runs the configured formatters over fixed files, so compare
	// gofmt clean on both sides.
	got, err := format.Source(fixed)
	if err != nil {
		t.Fatalf("fixed file does not parse: %v\n%s", err, fixed)
	}
	want, err := vs[0].Fix.Contents()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want[vs[0].Fix.SrcFile]) {
		t.Fatalf("fixed content disagrees with Contents:\n%s\nwant:\n%s", got, want[vs[0].Fix.SrcFile])
	}
}

func TestSuggestedEditsCrossFile(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	fset := token.NewFileSet()
	write(t, filepath.Join(dir, "a.go"), "package p\n\ntype Foo struct{}\n\nfunc NewFoo() *Foo { return nil }\n")
	a, err := parser.ParseFile(fset, filepath.Join(dir, "a.go"), "package p\n\ntype Foo struct{}\n\nfunc NewFoo() *Foo { return nil }\n", parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, "b.go"), "package p\n\nfunc (f *Foo) A() {}\n")
	b, err := parser.ParseFile(fset, filepath.Join(dir, "b.go"), "package p\n\nfunc (f *Foo) A() {}\n", parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	vs := CheckSyntax(fset, []*ast.File{a, b})
	if len(vs) != 1 || vs[0].Fix == nil {
		t.Fatalf("expected one violation with a fix, got %+v", vs)
	}
	if vs[0].Fix.SrcFile == vs[0].Fix.DstFile {
		t.Fatal("expected a two-file fix")
	}
	if _, ok := suggestedEdits(fset, vs[0]); ok {
		t.Fatal("two-file fixes must not carry suggested edits")
	}
}

func TestRunReports(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filepath.Join(dir, "a.go"), `package p

type Foo struct{}

func helper() {}

func (f *Foo) A() {}
`, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	pass := &analysis.Pass{
		Analyzer: &analysis.Analyzer{Name: "methodfilecheck"},
		Fset:     fset,
		Files:    []*ast.File{f},
		Report: func(d analysis.Diagnostic) {
			got = append(got, d.Message)
		},
	}
	out, err := pluginRun(pass)
	if err != nil || out != nil {
		t.Fatalf("got %v, %v", out, err)
	}
	if len(got) != 1 || !strings.Contains(got[0], "method Foo.A: type declared at line 3") {
		t.Fatalf("got %q", got)
	}
	// Generated files are not reported.
	gen, err := parser.ParseFile(fset, filepath.Join(dir, "gen.go"), `// Code generated by x DO NOT EDIT.
package p

func (f *Foo) B() {}
`, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	got = nil
	pass.Files = []*ast.File{f, gen}
	if _, err := pluginRun(pass); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %q", got)
	}
}
