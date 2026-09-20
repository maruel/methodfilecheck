// Copyright 2026 Marc-Antoine Ruel. All Rights Reserved. Use of this
// source code is governed by the Apache v2 license that can be found in the
// LICENSE file.

// Command methodfilecheck reports methods whose receiver type is declared in
// another source file in the same package, and method blocks interleaved with
// other declarations. Constructors, free functions whose first result is T or
// *T, may sit between type T and its methods.
//
// Usage: methodfilecheck [-fix] [patterns...]
package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/maruel/methodfilecheck"
)

func main() {
	fix := flag.Bool("fix", false, "reorder the source files to resolve the violations")
	flag.Parse()
	if err := run(os.Stderr, flag.Args(), *fix); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// run checks the packages matched by patterns, or fixes them in place, and
// writes one diagnostic line per remaining violation to stdout.
func run(out io.Writer, patterns []string, fix bool) error {
	pkgs, err := methodfilecheck.ListPackages(patterns)
	if err != nil {
		return err
	}
	var violations int
	for _, pkg := range pkgs {
		var vs []methodfilecheck.Violation
		if fix {
			if vs, err = methodfilecheck.FixPackage(pkg); err != nil {
				return err
			}
		} else {
			if vs, err = methodfilecheck.Check(pkg); err != nil {
				return err
			}
		}
		for i := range vs {
			if _, err := fmt.Fprintln(out, vs[i].String()); err != nil {
				return err
			}
		}
		violations += len(vs)
	}
	if violations != 0 {
		return fmt.Errorf("found %d method file placement violation(s)", violations)
	}
	return nil
}
