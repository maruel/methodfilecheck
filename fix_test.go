// Copyright 2026 Marc-Antoine Ruel. All Rights Reserved. Use of this
// source code is governed by the Apache v2 license that can be found in the
// LICENSE file.

package methodfilecheck

import (
	"bytes"
	"go/format"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestFixPackage(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		files := map[string]string{"a.go": `package p

type Foo struct{}

func (f *Foo) A() {}
`}
		pkg := writeFiles(t, files)
		if _, err := FixPackage(pkg); err != nil {
			t.Fatal(err)
		}
		wantFiles(t, dirOf(t, pkg), files)
	})
	t.Run("error", func(t *testing.T) {
		cases := []struct {
			name  string
			files map[string]string
			want  map[string]string
		}{
			{
				"free function between type and methods",
				map[string]string{"a.go": `package p

type Foo struct{}

func helper() {}

func (f *Foo) A() {}
`},
				map[string]string{"a.go": `package p

type Foo struct{}

func (f *Foo) A() {}

func helper() {}
`},
			},
			{
				"method before type",
				map[string]string{"a.go": `package p

func NewFoo() *Foo { return nil }

func (f *Foo) A() {}

type Foo struct{}
`},
				map[string]string{"a.go": `package p

func NewFoo() *Foo { return nil }

type Foo struct{}

func (f *Foo) A() {}
`},
			},
			{
				"interleaved receivers",
				map[string]string{"a.go": `package p

type Foo struct{}

func (f *Foo) A() {}

type Bar struct{}

func (b *Bar) A() {}

func (f *Foo) B() {}
`},
				map[string]string{"a.go": `package p

type Foo struct{}

func (f *Foo) A() {}

func (f *Foo) B() {}

type Bar struct{}

func (b *Bar) A() {}
`},
			},
			{
				"cross file with constructors",
				map[string]string{
					"a.go": `package p

type Foo struct{}

func NewFoo() *Foo { return nil }
`,
					"b.go": `package p

func (f *Foo) A() {}
`,
				},
				map[string]string{
					"a.go": `package p

type Foo struct{}

func NewFoo() *Foo { return nil }

func (f *Foo) A() {}
`,
					"b.go": `package p
`,
				},
			},
			{
				"cross file keeps exported before unexported",
				map[string]string{
					"a.go": `package p

type Foo struct{}

func (f *Foo) a() {}

func (f *Foo) b() {}
`,
					"b.go": `package p

func (f *Foo) C() {}
`,
				},
				map[string]string{
					"a.go": `package p

type Foo struct{}

func (f *Foo) C() {}

func (f *Foo) a() {}

func (f *Foo) b() {}
`,
					"b.go": `package p
`,
				},
			},
			{
				"interleaved with doc comments",
				map[string]string{"a.go": `package p

type Foo struct{}

// A does things.
func (f *Foo) A() {}

type Bar struct{}

// B does other things.
func (b *Bar) B() {}

// C does more.
func (f *Foo) C() {}
`},
				map[string]string{"a.go": `package p

type Foo struct{}

// A does things.
func (f *Foo) A() {}

// C does more.
func (f *Foo) C() {}

type Bar struct{}

// B does other things.
func (b *Bar) B() {}
`},
			},
			{
				"constructor after methods moves below the type",
				map[string]string{"a.go": `package p

type Foo struct{}

// A does things.
func (f *Foo) A() {}

// NewFoo builds a Foo.
func NewFoo() *Foo { return nil }
`},
				map[string]string{"a.go": `package p

type Foo struct{}

// NewFoo builds a Foo.
func NewFoo() *Foo { return nil }

// A does things.
func (f *Foo) A() {}
`},
			},
			{
				"constructor pulled next to its type",
				map[string]string{"a.go": `package p

type Foo struct{}

type Bar struct{}

func NewFoo() *Foo { return nil }

func helper() {}

func (f *Foo) A() {}
`},
				map[string]string{"a.go": `package p

type Foo struct{}

func NewFoo() *Foo { return nil }

func (f *Foo) A() {}

type Bar struct{}

func helper() {}
`},
			},
			{
				"method after grouped type decl",
				map[string]string{"a.go": `package p

type (
	Foo struct{}
	Bar struct{}
)

func NewFoo() *Foo { return nil }

func NewBar() *Bar { return nil }

func (f *Foo) A() {}
`},
				map[string]string{"a.go": `package p

type (
	Foo struct{}
	Bar struct{}
)

func NewFoo() *Foo { return nil }

func (f *Foo) A() {}

func NewBar() *Bar { return nil }
`},
			},
		}
		for _, c := range cases {
			t.Run(c.name, func(t *testing.T) {
				pkg := writeFiles(t, c.files)
				vs, err := FixPackage(pkg)
				if err != nil {
					t.Fatalf("violations remaining:\n%s\nerr: %v", joinViolations(vs), err)
				}
				dir := dirOf(t, pkg)
				wantFiles(t, dir, c.want)
				// Fixing again changes nothing.
				vs, err = FixPackage(pkg)
				if err != nil || len(vs) != 0 {
					t.Fatalf("not idempotent: %v, %v", vs, err)
				}
				wantFiles(t, dir, c.want)
			})
		}
	})
}

func TestLineEditMatchesContents(t *testing.T) {
	// For every same-file fix, splicing the LineEdit ranges into the file must
	// produce the same result as Contents.
	cases := []struct {
		name  string
		files map[string]string
	}{
		{"free function between type and methods", map[string]string{"a.go": "package p\n\ntype Foo struct{}\n\nfunc helper() {}\n\nfunc (f *Foo) A() {}\n"}},
		{"method before type", map[string]string{"a.go": "package p\n\nfunc (f *Foo) A() {}\n\ntype Foo struct{}\n"}},
		{"constructor after methods", map[string]string{"a.go": "package p\n\ntype Foo struct{}\n\nfunc (f *Foo) A() {}\n\nfunc NewFoo() *Foo { return nil }\n"}},
		{"interleaved receivers", map[string]string{"a.go": "package p\n\ntype Foo struct{}\n\nfunc (f *Foo) A() {}\n\ntype Bar struct{}\n\nfunc (b *Bar) A() {}\n\nfunc (f *Foo) B() {}\n"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			pkg := writeFiles(t, c.files)
			vs, err := Check(pkg)
			if err != nil || len(vs) == 0 {
				t.Fatalf("expected violations, got %v, %v", vs, err)
			}
			for _, v := range vs {
				if v.Fix == nil || v.Fix.SrcFile != v.Fix.DstFile {
					continue
				}
				start, end, replacement, ok := v.Fix.LineEdit()
				if !ok {
					t.Fatal("LineEdit returned false for a same-file fix")
				}
				lines := strings.Split(strings.TrimSuffix(read(t, v.Fix.SrcFile), "\n"), "\n")
				var out []string
				out = append(out, lines[:start-1]...)
				out = append(out, replacement...)
				out = append(out, lines[end:]...)
				want, err := v.Fix.Contents()
				if err != nil {
					t.Fatal(err)
				}
				got, err := format.Source([]byte(strings.Join(out, "\n") + "\n"))
				if err != nil {
					t.Fatalf("spliced file does not parse: %v\n%s", err, strings.Join(out, "\n"))
				}
				if !bytes.Equal(got, want[v.Fix.SrcFile]) {
					t.Fatalf("LineEdit splice disagrees with Contents:\n%s\nwant:\n%s", got, want[v.Fix.SrcFile])
				}
			}
		})
	}
}

func TestFixPackagePreservesFileMode(t *testing.T) {
	pkg := writeFiles(t, map[string]string{"a.go": "package p\n\ntype Foo struct{}\n\nfunc helper() {}\n\nfunc (f *Foo) A() {}\n"})
	path := filepath.Join(pkg.Dir, "a.go")
	if err := os.Chmod(path, 0o644); err != nil { //nolint:gosec // testing that modes are preserved.
		t.Fatal(err)
	}
	if _, err := FixPackage(pkg); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o644 {
		t.Fatalf("got mode %v, want 0o644", got)
	}
}

func TestFixPackageBatchesAcrossFiles(t *testing.T) {
	pkg := writeFiles(t, map[string]string{
		"a.go": "package p\n\ntype Foo struct{}\n\nfunc helper() {}\n\nfunc (f *Foo) A() {}\n",
		"b.go": "package p\n\ntype Bar struct{}\n\nfunc helper2() {}\n\nfunc (b *Bar) B() {}\n",
	})
	vs, err := FixPackage(pkg)
	if err != nil {
		t.Fatalf("violations remaining:\n%s\nerr: %v", joinViolations(vs), err)
	}
	for _, name := range []string{"a.go", "b.go"} {
		out := read(t, filepath.Join(pkg.Dir, name))
		if !strings.Contains(out, "type Foo struct{}\n\nfunc (f *Foo) A() {}") && !strings.Contains(out, "type Bar struct{}\n\nfunc (b *Bar) B() {}") {
			t.Fatalf("%s was not fixed:\n%s", name, out)
		}
	}
}

func TestFixPackageCycles(t *testing.T) {
	// Two types in one grouped decl each with methods cannot both be adjacent
	// to their type; the fixer must stop instead of looping forever.
	pkg := writeFiles(t, map[string]string{"a.go": `package p

type (
	Foo struct{}
	Bar struct{}
)

func (f *Foo) A() {}

func (b *Bar) A() {}
`})
	vs, err := FixPackage(pkg)
	if err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("expected a cycle error, got %v (violations: %s)", err, joinViolations(vs))
	}
	if len(vs) == 0 {
		t.Fatal("expected remaining violations")
	}
}

func TestFixPackageImports(t *testing.T) {
	if _, err := exec.LookPath("goimports"); err != nil {
		t.Skip("goimports is not installed")
	}
	pkg := writeFiles(t, map[string]string{
		"a.go": `package p

type Foo struct{}

func NewFoo() *Foo { return nil }
`,
		"b.go": `package p

import "fmt"

func (f *Foo) A() string { return fmt.Sprint("A") }
`,
	})
	vs, err := FixPackage(pkg)
	if err != nil {
		t.Fatalf("violations remaining:\n%s\nerr: %v", joinViolations(vs), err)
	}
	out := read(t, filepath.Join(dirOf(t, pkg), "a.go"))
	if !strings.Contains(out, `"fmt"`) {
		t.Fatalf("a.go is missing the fmt import:\n%s", out)
	}
	if out := read(t, filepath.Join(dirOf(t, pkg), "b.go")); strings.Contains(out, `"fmt"`) {
		t.Fatalf("b.go still imports fmt:\n%s", out)
	}
}

func TestRunGoimports(t *testing.T) {
	if _, err := exec.LookPath("goimports"); err != nil {
		t.Skip("goimports is not installed")
	}
	path := filepath.Join(t.TempDir(), "a.go")
	if err := os.WriteFile(path, []byte("package p\n\nvar _ = fmt.Stringer(nil)\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := runGoimports([]string{path}); err != nil {
		t.Fatal(err)
	}
	if out := read(t, path); !strings.Contains(out, `"fmt"`) {
		t.Fatalf("goimports did not add the import:\n%s", out)
	}
	if err := runGoimports(nil); err != nil {
		t.Fatal(err)
	}
}

func TestApplyFixInvalid(t *testing.T) {
	if err := applyFix(&Fix{SrcFile: filepath.Join(t.TempDir(), "nope.go"), SrcStart: 1, SrcEnd: 1, DstFile: "x.go", DstLine: 1}); err == nil {
		t.Error("expected a read error")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "a.go")
	if err := os.WriteFile(path, []byte("package p\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cases := []Fix{
		{SrcFile: path, SrcStart: 5, SrcEnd: 6, DstFile: path, DstLine: 1},
		{SrcFile: path, SrcStart: 2, SrcEnd: 1, DstFile: path, DstLine: 1},
	}
	for _, f := range cases {
		if err := applyFix(&f); err == nil {
			t.Errorf("%+v: expected an error", f)
		}
	}
	// Moving to a file that does not parse after the edit fails without
	// touching the destination.
	dst := filepath.Join(dir, "b.go")
	if err := os.WriteFile(dst, []byte("package p\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	f := Fix{SrcFile: path, SrcStart: 1, SrcEnd: 1, DstFile: dst, DstLine: 0}
	if err := applyFix(&f); err == nil {
		t.Error("inserting before the package clause should fail")
	}
}

func TestContentsInvalid(t *testing.T) {
	f := &Fix{SrcFile: filepath.Join(t.TempDir(), "a.go"), SrcStart: 1, SrcEnd: 2, DstFile: "a.go", DstLine: 1}
	if _, err := f.Contents(); err == nil {
		t.Error("expected a format error")
	}
}

// writeFiles writes the sources and changes into their directory.
func writeFiles(t *testing.T, files map[string]string) Package {
	t.Helper()
	return pkgFromFiles(t, files)
}

func dirOf(t *testing.T, pkg Package) string {
	t.Helper()
	return pkg.Dir
}

func wantFiles(t *testing.T, dir string, want map[string]string) {
	t.Helper()
	names := make([]string, 0, len(want))
	for name := range want {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		if got := read(t, filepath.Join(dir, name)); got != want[name] {
			t.Errorf("%s:\ngot:\n%s\nwant:\n%s", name, got, want[name])
		}
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	out, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func joinViolations(vs []Violation) string {
	out := make([]string, len(vs))
	for i, v := range vs {
		out[i] = v.String()
	}
	return strings.Join(out, "\n")
}
