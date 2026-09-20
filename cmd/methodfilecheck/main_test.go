// Copyright 2026 Marc-Antoine Ruel. All Rights Reserved. Use of this
// source code is governed by the Apache v2 license that can be found in the
// LICENSE file.

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeModule writes a one-package module and changes into it.
func writeModule(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	t.Chdir(dir)
	all := map[string]string{"go.mod": "module p\n\ngo 1.27\n"}
	for name, src := range files {
		all[name] = src
	}
	for name, src := range all {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestRun(t *testing.T) {
	valid := map[string]string{"a.go": "package p\n\ntype Foo struct{}\n\nfunc (f *Foo) A() {}\n"}
	t.Run("valid", func(t *testing.T) {
		writeModule(t, valid)
		var out bytes.Buffer
		if err := run(&out, nil, false); err != nil {
			t.Fatal(err)
		}
		if out.Len() != 0 {
			t.Fatalf("got %q", out.String())
		}
	})
	t.Run("violations", func(t *testing.T) {
		writeModule(t, map[string]string{
			"a.go": "package p\n\ntype Foo struct{}\n\nfunc helper() {}\n\nfunc (f *Foo) A() {}\n",
		})
		var out bytes.Buffer
		err := run(&out, nil, false)
		if err == nil || !strings.Contains(err.Error(), "1 method file placement violation") {
			t.Fatalf("got %v", err)
		}
		if !strings.Contains(err.Error(), "re-run with -fix") {
			t.Fatalf("error should suggest -fix, got %v", err)
		}
		if got, want := out.String(), "a.go:7: method Foo.A: type declared at line 3"; !strings.HasPrefix(got, want) {
			t.Fatalf("got %q, want prefix %q", got, want)
		}
	})
	t.Run("fix", func(t *testing.T) {
		dir := writeModule(t, map[string]string{
			"a.go": "package p\n\ntype Foo struct{}\n\nfunc helper() {}\n\nfunc (f *Foo) A() {}\n",
		})
		var out bytes.Buffer
		if err := run(&out, nil, true); err != nil {
			t.Fatal(err)
		}
		if out.Len() != 0 {
			t.Fatalf("got %q", out.String())
		}
		src, err := os.ReadFile(filepath.Join(dir, "a.go"))
		if err != nil {
			t.Fatal(err)
		}
		if want := "type Foo struct{}\n\nfunc (f *Foo) A() {}\n\nfunc helper() {}\n"; !strings.Contains(string(src), want) {
			t.Fatalf("got:\n%s", src)
		}
	})
	t.Run("fix stuck", func(t *testing.T) {
		// Two types in one grouped decl each with methods cannot both be
		// adjacent to their type; the fixer must stop and say so.
		writeModule(t, map[string]string{
			"a.go": "package p\n\ntype (\n\tFoo struct{}\n\tBar struct{}\n)\n\nfunc (f *Foo) A() {}\n\nfunc (b *Bar) A() {}\n",
		})
		if err := run(&bytes.Buffer{}, nil, true); err == nil || !strings.Contains(err.Error(), "cycle") {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("bad pattern", func(t *testing.T) {
		writeModule(t, valid)
		if err := run(&bytes.Buffer{}, []string{"./nope"}, false); err == nil {
			t.Fatal("expected an error")
		}
	})
}
