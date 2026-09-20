// Copyright 2026 Marc-Antoine Ruel. All Rights Reserved. Use of this
// source code is governed by the Apache v2 license that can be found in the
// LICENSE file.

// Package methodfilecheck reports methods whose receiver type is declared in
// another source file in the same package, and method blocks interleaved with
// other declarations. Constructors — free functions whose first result is T or
// *T — may sit between type T and its methods, and must come before T's first
// method so that the type, its constructors, and its methods stay together.
//
// Violations come with a [Fix] that moves the misplaced block of code, so the
// whole check is auto-fixable with [Fix].
package methodfilecheck

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

// Package describes one Go package to check or fix.
type Package struct {
	ImportPath   string
	Dir          string
	GoFiles      []string
	CgoFiles     []string
	TestGoFiles  []string
	XTestGoFiles []string
}

// ListPackages resolves the patterns like `go list` does and returns each
// package, including its in-package test files. XTest files are checked as
// their own package group, since they cannot hold methods on the outer
// package's types.
func ListPackages(patterns []string) ([]Package, error) {
	if len(patterns) == 0 {
		patterns = []string{"./..."}
	}
	args := append([]string{"list", "-json"}, patterns...)
	cmd := exec.Command("go", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("go %s: %w\n%s", strings.Join(args, " "), err, bytes.TrimSpace(out))
	}
	dec := json.NewDecoder(bytes.NewReader(out))
	var pkgs []Package
	for {
		var raw struct {
			Package
			Error *struct{ Err string }
		}
		if err := dec.Decode(&raw); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, fmt.Errorf("decode go list JSON: %w", err)
		}
		if raw.Error != nil {
			return nil, fmt.Errorf("%s: %s", raw.ImportPath, raw.Error.Err)
		}
		pkgs = append(pkgs, raw.Package)
	}
	return pkgs, nil
}

// Check parses the package files and returns one Violation per misplaced
// method run, sorted by file then line.
func Check(pkg Package) ([]Violation, error) {
	fset := token.NewFileSet()
	var violations []Violation
	for _, group := range []struct {
		names []string
		xtest bool
	}{
		{pkg.GoFiles, false},
		{pkg.CgoFiles, false},
		{pkg.TestGoFiles, false},
		{pkg.XTestGoFiles, true},
	} {
		var files []*file
		for _, name := range group.names {
			path := filepath.Join(pkg.Dir, name)
			f, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
			if err != nil {
				return nil, fmt.Errorf("parse %s: %w", path, err)
			}
			if ast.IsGenerated(f) {
				continue
			}
			files = append(files, newFile(fset, f))
		}
		violations = append(violations, checkGroup(files)...)
	}
	slices.SortFunc(violations, func(a, b Violation) int {
		if c := strings.Compare(a.File, b.File); c != 0 {
			return c
		}
		return a.Line - b.Line
	})
	return violations, nil
}

// CheckSyntax reports violations for already parsed files belonging to one
// package, skipping generated files. It is meant for integrations like
// golangci-lint module plugins that parse the code themselves. The file names
// in fset must be absolute. It does not sort the violations.
func CheckSyntax(fset *token.FileSet, files []*ast.File) []Violation {
	var parsed []*file
	for _, f := range files {
		if ast.IsGenerated(f) {
			continue
		}
		parsed = append(parsed, newFile(fset, f))
	}
	return checkGroup(parsed)
}
