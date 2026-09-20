// Copyright 2026 Marc-Antoine Ruel. All Rights Reserved. Use of this
// source code is governed by the Apache v2 license that can be found in the
// LICENSE file.

package methodfilecheck

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"go/format"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

// maxFixPasses bounds the fix loop. Each pass applies one fix, which resolves
// at least one violation without introducing new ones, so real fixpoint is
// reached well below this bound.
const maxFixPasses = 128

// FixPackage reorders the package files on disk until [Check] reports no violation.
// It returns the violations that remain when it cannot make progress. Each
// moved block keeps its doc comment, the edited files are rewritten gofmt
// clean, and goimports is run on them when available so imports follow the
// moved code.
func FixPackage(pkg Package) ([]Violation, error) {
	var touched []string
	seen := map[string]struct{}{}
	for range maxFixPasses {
		vs, err := Check(pkg)
		if err != nil {
			return nil, err
		}
		if len(vs) == 0 {
			return nil, runGoimports(touched)
		}
		state, err := hashPackage(pkg)
		if err != nil {
			return vs, err
		}
		if _, ok := seen[state]; ok {
			return vs, errors.New("fixes cycle without converging; resolve the remaining violations by hand")
		}
		seen[state] = struct{}{}
		fix := vs[0].Fix
		for _, v := range vs {
			if v.Fix != nil {
				fix = v.Fix
				break
			}
		}
		if fix == nil {
			return vs, nil
		}
		if err := applyFix(fix); err != nil {
			return vs, err
		}
		for _, p := range []string{fix.SrcFile, fix.DstFile} {
			if !slices.Contains(touched, p) {
				touched = append(touched, p)
			}
		}
	}
	vs, err := Check(pkg)
	if err != nil {
		return nil, err
	}
	return vs, fmt.Errorf("did not converge after %d fix passes", maxFixPasses)
}

// hashPackage summarizes the package source files so repeated states, e.g.
// two methods of one grouped type decl fighting over the same insertion
// point, can be detected.
func hashPackage(pkg Package) (string, error) {
	h := sha256.New()
	for _, name := range append(slices.Clone(pkg.GoFiles), append(slices.Clone(pkg.CgoFiles), append(slices.Clone(pkg.TestGoFiles), pkg.XTestGoFiles...)...)...) {
		src, err := os.ReadFile(filepath.Join(pkg.Dir, name))
		if err != nil {
			return "", err
		}
		fmt.Fprintf(h, "%s\x00%d\x00", name, len(src)) //nolint:errcheck // writes to a hash
		h.Write(src)
	}
	return string(h.Sum(nil)), nil
}

// applyFix moves the block of lines described by fix between the files. The
// destination file is rewritten gofmt clean or not at all.
func applyFix(fix *Fix) error {
	srcLines, err := readLines(fix.SrcFile)
	if err != nil {
		return err
	}
	if fix.SrcStart < 1 || fix.SrcStart > fix.SrcEnd || fix.SrcEnd > len(srcLines) {
		return fmt.Errorf("%s: invalid block to move: lines %d-%d of %d", fix.SrcFile, fix.SrcStart, fix.SrcEnd, len(srcLines))
	}
	block := slices.Clone(srcLines[fix.SrcStart-1 : fix.SrcEnd])
	if fix.SrcFile == fix.DstFile {
		idx := insertIndex(fix, len(srcLines))
		// Adjust the insertion point for the lines removed below.
		shift := min(max(idx, fix.SrcStart-1), fix.SrcEnd) - (fix.SrcStart - 1)
		rest := slices.Delete(slices.Clone(srcLines), fix.SrcStart-1, fix.SrcEnd)
		return writeFile(fix.DstFile, insertBlock(rest, idx-shift, block))
	}
	dstLines, err := readLines(fix.DstFile)
	if err != nil {
		return err
	}
	idx := insertIndex(fix, len(dstLines))
	if err := writeFile(fix.DstFile, insertBlock(dstLines, idx, block)); err != nil {
		return err
	}
	rest := slices.Delete(slices.Clone(srcLines), fix.SrcStart-1, fix.SrcEnd)
	return writeFile(fix.SrcFile, rest)
}

// insertIndex returns the 0-based index in lines before which the block must
// be inserted. Above inserts before the anchor line, below after it.
func insertIndex(fix *Fix, size int) int {
	idx := fix.DstLine
	if fix.Above {
		idx = fix.DstLine - 1
	}
	return min(max(idx, 0), size)
}

// insertBlock splices block into lines at idx, adding blank lines so the
// block stays separated from the surrounding declarations.
func insertBlock(lines []string, idx int, block []string) []string {
	if idx > 0 && lines[idx-1] != "" {
		block = append([]string{""}, block...)
	}
	if idx < len(lines) && lines[idx] != "" {
		block = append(block, "")
	}
	return slices.Insert(lines, idx, block...)
}

// writeFile writes gofmt-cleaned lines if the result still parses, and
// leaves the file untouched otherwise.
func writeFile(path string, lines []string) error {
	formatted, err := format.Source([]byte(strings.Join(lines, "\n") + "\n"))
	if err != nil {
		return fmt.Errorf("format %s after edit: %w", path, err)
	}
	return os.WriteFile(path, formatted, 0o600)
}

func readLines(path string) ([]string, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	lines := bytes.Split(bytes.TrimSuffix(src, []byte("\n")), []byte("\n"))
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = string(l)
	}
	return out, nil
}

// runGoimports rewrites the touched files with goimports when it is installed
// so imports follow the moved code. Missing goimports is not an error.
func runGoimports(paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	bin, err := exec.LookPath("goimports")
	if err != nil {
		return nil //nolint:nilerr // goimports is optional
	}
	args := append([]string{"-w"}, paths...)
	if out, err := exec.Command(bin, args...).CombinedOutput(); err != nil {
		return fmt.Errorf("goimports: %w\n%s", err, bytes.TrimSpace(out))
	}
	return nil
}
