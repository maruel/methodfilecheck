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

// maxFixPasses bounds the fix loop. Each pass applies every fix whose files do
// not collide with another applied fix, so the number of passes stays well
// below this bound.
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
		// Apply every fix whose files no other applied fix touches this pass;
		// line numbers of untouched files stay valid.
		used := map[string]bool{}
		applied := 0
		for i := range vs {
			fix := vs[i].Fix
			if fix == nil || used[fix.SrcFile] || used[fix.DstFile] {
				continue
			}
			if err := applyFix(fix); err != nil {
				return vs, err
			}
			used[fix.SrcFile], used[fix.DstFile] = true, true
			for _, p := range []string{fix.SrcFile, fix.DstFile} {
				if !slices.Contains(touched, p) {
					touched = append(touched, p)
				}
			}
			applied++
		}
		if applied == 0 {
			return vs, errors.New("no fix can be applied; resolve the remaining violations by hand")
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
	contents, err := fix.Contents()
	if err != nil {
		return err
	}
	for path, content := range contents {
		mode := os.FileMode(0o644)
		if info, err := os.Stat(path); err == nil {
			mode = info.Mode().Perm()
		}
		if err := os.WriteFile(path, content, mode); err != nil {
			return err
		}
	}
	return nil
}

// Fix moves a block of lines to make the code pass the check.
type Fix struct {
	SrcFile          string // absolute path of the file holding the block
	SrcStart, SrcEnd int    // 1-based inclusive line range to move, doc comment included
	DstFile          string // absolute path of the destination file
	DstLine          int    // 1-based anchor line in DstFile, doc comment included when Above
	Above            bool   // insert above DstLine instead of below
}

// LineEdit describes a same-file fix in original file coordinates: the lines
// [start, end] are replaced by replacement, which relocates the misplaced
// block, blank line padding included, to its destination. ok is false for
// two-file fixes, which golangci-lint's fixer cannot apply, and when the fix
// is stale.
func (fix *Fix) LineEdit() (start, end int, replacement []string, ok bool) {
	if fix.SrcFile != fix.DstFile {
		return 0, 0, nil, false
	}
	lines, err := readLines(fix.SrcFile)
	if err != nil || fix.SrcStart < 1 || fix.SrcStart > fix.SrcEnd || fix.SrcEnd > len(lines) {
		return 0, 0, nil, false
	}
	block := slices.Clone(lines[fix.SrcStart-1 : fix.SrcEnd])
	orig := insertIndex(fix, len(lines))
	shift := min(max(orig, fix.SrcStart-1), fix.SrcEnd) - (fix.SrcStart - 1)
	idx := orig - shift
	rest := slices.Delete(slices.Clone(lines), fix.SrcStart-1, fix.SrcEnd)
	padded := paddedBlock(rest, idx, block)
	switch {
	case idx == fix.SrcStart-1:
		return 0, 0, nil, false // already in place
	case idx < fix.SrcStart-1: // the block moves up
		start, end = idx+1, fix.SrcEnd
		replacement = append(slices.Clone(padded), lines[idx:fix.SrcStart-1]...)
	default: // the block moves down
		start, end = fix.SrcStart, orig
		replacement = append(slices.Clone(lines[fix.SrcEnd:orig]), padded...)
	}
	return start, end, replacement, true
}

// Contents returns the gofmt-clean replacement contents for the files the fix
// touches, keyed by absolute path. The files on disk are left untouched.
func (fix *Fix) Contents() (map[string][]byte, error) {
	srcLines, err := readLines(fix.SrcFile)
	if err != nil {
		return nil, err
	}
	if fix.SrcStart < 1 || fix.SrcStart > fix.SrcEnd || fix.SrcEnd > len(srcLines) {
		return nil, fmt.Errorf("%s: invalid block to move: lines %d-%d of %d", fix.SrcFile, fix.SrcStart, fix.SrcEnd, len(srcLines))
	}
	block := slices.Clone(srcLines[fix.SrcStart-1 : fix.SrcEnd])
	if fix.SrcFile == fix.DstFile {
		idx := insertIndex(fix, len(srcLines))
		// Adjust the insertion point for the lines removed below.
		shift := min(max(idx, fix.SrcStart-1), fix.SrcEnd) - (fix.SrcStart - 1)
		rest := slices.Delete(slices.Clone(srcLines), fix.SrcStart-1, fix.SrcEnd)
		content, err := formatLines(fix.DstFile, insertBlock(rest, idx-shift, block))
		if err != nil {
			return nil, err
		}
		return map[string][]byte{fix.DstFile: content}, nil
	}
	dstLines, err := readLines(fix.DstFile)
	if err != nil {
		return nil, err
	}
	idx := insertIndex(fix, len(dstLines))
	dst, err := formatLines(fix.DstFile, insertBlock(dstLines, idx, block))
	if err != nil {
		return nil, err
	}
	src, err := formatLines(fix.SrcFile, slices.Delete(slices.Clone(srcLines), fix.SrcStart-1, fix.SrcEnd))
	if err != nil {
		return nil, err
	}
	return map[string][]byte{fix.DstFile: dst, fix.SrcFile: src}, nil
}

// formatLines validates that the edited lines still parse and returns the file
// contents gofmt clean.
func formatLines(path string, lines []string) ([]byte, error) {
	formatted, err := format.Source([]byte(strings.Join(lines, "\n") + "\n"))
	if err != nil {
		return nil, fmt.Errorf("format %s after edit: %w", path, err)
	}
	return formatted, nil
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

// paddedBlock adds blank lines around block so it stays separated from the
// lines before and after its insertion point idx.
func paddedBlock(lines []string, idx int, block []string) []string {
	if idx > 0 && lines[idx-1] != "" {
		block = append([]string{""}, block...)
	}
	if idx < len(lines) && lines[idx] != "" {
		block = append(block, "")
	}
	return block
}

// insertBlock splices block into lines at idx, adding blank lines so the
// block stays separated from the surrounding declarations.
func insertBlock(lines []string, idx int, block []string) []string {
	return slices.Insert(lines, idx, paddedBlock(lines, idx, block)...)
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
