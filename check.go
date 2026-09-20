// Copyright 2026 Marc-Antoine Ruel. All Rights Reserved. Use of this
// source code is governed by the Apache v2 license that can be found in the
// LICENSE file.

package methodfilecheck

import (
	"fmt"
	"go/ast"
	"go/token"
	"path/filepath"
	"strings"
)

// Violation is one method placement violation. Fix is nil when no automatic
// move is known.
type Violation struct {
	File    string // absolute path of the misplaced method
	Line    int    // 1-based line of the misplaced method
	Pos     token.Pos
	Message string
	Fix     *Fix
}

// crossFileViolation reports a run whose receiver type is declared in another
// file of the same package.
func crossFileViolation(r *run, t *typeDecl, b *block) *Violation {
	fix := &Fix{SrcFile: r.file.path, SrcStart: r.start, SrcEnd: r.end}
	var reason string
	switch {
	case b == nil || b.lastEnd == 0:
		fix.DstFile, fix.DstLine = t.file.path, t.endLine
		reason = "after " + r.receiver + " declaration and any constructors"
	case !r.anyExp:
		fix.DstFile, fix.DstLine = t.file.path, b.lastEnd
		reason = "after last " + r.receiver + " method"
	case b.lastExp != 0:
		fix.DstFile, fix.DstLine = t.file.path, b.lastExp
		reason = "after last exported " + r.receiver + " method"
	default:
		fix.DstFile, fix.DstLine, fix.Above = t.file.path, b.firstDoc, true
		reason = "before " + r.receiver + " methods (exported first)"
	}
	verb := "after"
	if fix.Above {
		verb = "above"
	}
	return &Violation{
		File: r.file.path,
		Line: r.first,
		Pos:  r.fn.Pos(),
		Message: fmt.Sprintf("method %s.%s: move %s %s:%d (%s)", r.receiver,
			r.fn.Name.Name, verb, rel(fix.DstFile), fix.DstLine, reason),
		Fix: fix,
	}
}

// String renders the violation as a diagnostic line with a path relative to
// the working directory when possible.
func (v *Violation) String() string {
	return fmt.Sprintf("%s:%d: %s", rel(v.File), v.Line, v.Message)
}

// file is one parsed source file of a package group.
type file struct {
	fset    *token.FileSet
	path    string // absolute, cleaned
	f       *ast.File
	guarded bool // carries a //go:build constraint
}

func newFile(fset *token.FileSet, f *ast.File) *file {
	return &file{
		fset:    fset,
		path:    cleanPath(fset.Position(f.Pos()).Filename),
		f:       f,
		guarded: hasBuildConstraint(f),
	}
}

// run is a maximal sequence of methods with the same receiver in one file.
// Free functions and constructors do not split a run; only another receiver's
// methods do.
type run struct {
	receiver string
	file     *file
	fn       *ast.FuncDecl // first method of the run
	start    int           // first line, doc comment of the first method included
	first    int           // line of the first func declaration
	end      int           // end line of the last method
	anyExp   bool          // has at least one exported method
	lastExp  int           // end line of the last exported method, 0 if none
}

// typeDecl is a declared type with the constructors that directly follow it.
type typeDecl struct {
	file    *file
	line    int // line of the type spec
	endLine int // end line of the type and its directly following constructors
}

// block summarizes a receiver's methods within one file, enough to pick a
// funcorder-safe insertion point (exported methods must precede unexported
// ones).
type block struct {
	firstDoc int // first line of the first method, doc comment included
	lastEnd  int // end line of the last method
	lastExp  int // end line of the last exported method, 0 if none
}

// checkGroup reports violations for one package group. All files must belong
// to the same package: methods of the external test package never move to the
// outer package's files, so callers must not mix them.
func checkGroup(files []*file) []Violation {
	types := map[string]*typeDecl{}
	runsByFile := map[*file][]*run{}
	var runs []*run
	for _, src := range files {
		collectTypes(src, types)
		rs := collectRuns(src)
		runsByFile[src] = rs
		runs = append(runs, rs...)
	}
	blocks := map[string]*block{}
	for _, r := range runs {
		key := r.receiver + "\x00" + r.file.path
		b := blocks[key]
		if b == nil {
			b = &block{firstDoc: r.start}
		}
		if r.end > b.lastEnd {
			b.lastEnd = r.end
		}
		if r.lastExp > b.lastExp {
			b.lastExp = r.lastExp
		}
		blocks[key] = b
	}
	var vs []Violation
	for _, src := range files {
		vs = append(vs, checkAdjacency(src, types, runsByFile[src])...)
		vs = append(vs, checkConstructors(src, types, runsByFile[src])...)
		vs = append(vs, checkInterleaved(src, runsByFile[src])...)
	}
	for _, r := range runs {
		t := types[r.receiver]
		if t == nil || t.file == r.file || t.file.guarded || r.file.guarded {
			continue
		}
		vs = append(vs, *crossFileViolation(r, t, blocks[r.receiver+"\x00"+t.file.path]))
	}
	return vs
}

// checkAdjacency reports each receiver whose first method in src does not
// directly follow the type declaration and its constructors, and fixes it by
// moving the receiver's first run right below the type's constructors.
func checkAdjacency(src *file, types map[string]*typeDecl, runs []*run) []Violation {
	typeIndexes := map[string]int{}
	typeLines := map[string]int{}
	for i, decl := range src.f.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.TYPE {
			continue
		}
		for _, spec := range gen.Specs {
			t := spec.(*ast.TypeSpec)
			typeIndexes[t.Name.Name] = i
			typeLines[t.Name.Name] = src.fset.Position(t.Pos()).Line
		}
	}
	firstRun := map[string]*run{}
	for _, r := range runs {
		if firstRun[r.receiver] == nil {
			firstRun[r.receiver] = r
		}
	}
	var vs []Violation
	seen := map[string]struct{}{}
	for i, decl := range src.f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Recv == nil || len(fn.Recv.List) == 0 {
			continue
		}
		receiver := receiverTypeName(fn.Recv.List[0].Type)
		if receiver == "" {
			continue
		}
		if _, ok := seen[receiver]; ok {
			continue
		}
		seen[receiver] = struct{}{}
		typeIndex, ok := typeIndexes[receiver]
		if !ok || i > typeIndex && onlyConstructors(src.f.Decls[typeIndex+1:i], receiver) {
			continue
		}
		v := Violation{
			File: src.path,
			Line: src.fset.Position(fn.Pos()).Line,
			Pos:  fn.Pos(),
			Message: fmt.Sprintf("method %s.%s: type declared at line %d is separated from its first method; keep the type, its constructors, and its complete method block together",
				receiver, fn.Name.Name, typeLines[receiver]),
		}
		if t := types[receiver]; t != nil {
			if r := firstRun[receiver]; r != nil {
				v.Fix = &Fix{
					SrcFile:  r.file.path,
					SrcStart: r.start,
					SrcEnd:   r.end,
					DstFile:  t.file.path,
					DstLine:  t.endLine,
				}
			}
		}
		vs = append(vs, v)
	}
	return vs
}

// checkConstructors reports each constructor of a type that sits after the
// type's first method in the same file, and fixes it by moving it directly
// below the type declaration and its leading constructors. Constructors keep
// their doc comment and stay ahead of the methods, the way funcorder orders
// them, so fixing stays compatible with funcorder-enabled projects.
func checkConstructors(src *file, types map[string]*typeDecl, runs []*run) []Violation {
	firstRun := map[string]*run{}
	for _, r := range runs {
		if firstRun[r.receiver] == nil {
			firstRun[r.receiver] = r
		}
	}
	var vs []Violation
	for _, d := range src.f.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok || fn.Recv != nil {
			continue
		}
		name := constructedType(fn)
		if name == "" {
			continue
		}
		t, ok := types[name]
		if !ok || t.file != src {
			continue
		}
		r := firstRun[name]
		if r == nil || src.fset.Position(fn.Pos()).Line < r.first {
			continue
		}
		vs = append(vs, Violation{
			File: src.path,
			Line: src.fset.Position(fn.Pos()).Line,
			Pos:  fn.Pos(),
			Message: fmt.Sprintf("constructor %s for %s: move after %s:%d (before %s methods)",
				fn.Name.Name, name, rel(t.file.path), t.endLine, name),
			Fix: &Fix{
				SrcFile:  src.path,
				SrcStart: docStart(src.fset, fn),
				SrcEnd:   src.fset.Position(fn.End()).Line,
				DstFile:  t.file.path,
				DstLine:  t.endLine,
			},
		})
	}
	return vs
}

// docStart returns the first line of the function declaration, doc comment
// included.
func docStart(fset *token.FileSet, fn *ast.FuncDecl) int {
	if fn.Doc != nil && len(fn.Doc.List) != 0 {
		return fset.Position(fn.Doc.Pos()).Line
	}
	return fset.Position(fn.Pos()).Line
}

// checkInterleaved reports each receiver run that reopens after another
// receiver's methods in the same file, and fixes it by moving the reopening
// run next to the previous run of the same receiver.
func checkInterleaved(src *file, runs []*run) []Violation {
	prev := map[string]*run{}
	var vs []Violation
	for _, r := range runs {
		p := prev[r.receiver]
		prev[r.receiver] = r
		if p == nil {
			continue
		}
		fix := &Fix{SrcFile: r.file.path, SrcStart: r.start, SrcEnd: r.end, DstFile: p.file.path}
		switch {
		case !r.anyExp:
			fix.DstLine = p.end
		case p.lastExp != 0:
			fix.DstLine = p.lastExp
		default:
			fix.DstLine, fix.Above = p.start, true
		}
		verb := "after"
		if fix.Above {
			verb = "above"
		}
		vs = append(vs, Violation{
			File: src.path,
			Line: r.first,
			Pos:  r.fn.Pos(),
			Message: fmt.Sprintf("method %s.%s: move %s %s:%d to keep %s methods contiguous",
				r.receiver, r.fn.Name.Name, verb, rel(fix.DstFile), fix.DstLine, r.receiver),
			Fix: fix,
		})
	}
	return vs
}

// collectTypes records each type declared in src, along with the constructors
// that directly follow it.
func collectTypes(src *file, types map[string]*typeDecl) {
	for i, decl := range src.f.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.TYPE {
			continue
		}
		for _, spec := range gen.Specs {
			ts := spec.(*ast.TypeSpec)
			if _, ok := types[ts.Name.Name]; ok {
				continue
			}
			end := src.fset.Position(gen.End()).Line // covers the closing paren of a grouped decl
			for _, next := range src.f.Decls[i+1:] {
				if constructedType(next) != ts.Name.Name {
					break
				}
				end = src.fset.Position(next.End()).Line
			}
			types[ts.Name.Name] = &typeDecl{file: src, line: src.fset.Position(ts.Pos()).Line, endLine: end}
		}
	}
}

// collectRuns splits the methods of src into maximal same-receiver runs.
func collectRuns(src *file) []*run {
	var runs []*run
	var current *run
	for _, decl := range src.f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Recv == nil || len(fn.Recv.List) == 0 {
			continue
		}
		receiver := receiverTypeName(fn.Recv.List[0].Type)
		if receiver == "" {
			continue
		}
		if current == nil || current.receiver != receiver {
			current = &run{receiver: receiver, file: src, fn: fn}
			current.first = src.fset.Position(fn.Pos()).Line
			current.start = current.first
			if fn.Doc != nil && len(fn.Doc.List) != 0 {
				current.start = src.fset.Position(fn.Doc.Pos()).Line
			}
			runs = append(runs, current)
		}
		current.end = src.fset.Position(fn.End()).Line
		if fn.Name.IsExported() {
			current.anyExp = true
			current.lastExp = current.end
		}
	}
	return runs
}

// onlyConstructors reports whether every decl constructs type name.
func onlyConstructors(decls []ast.Decl, name string) bool {
	for _, d := range decls {
		if constructedType(d) != name {
			return false
		}
	}
	return true
}

// constructedType returns T when decl is a free function whose first result
// is T or *T, and "" otherwise.
func constructedType(decl ast.Decl) string {
	fn, ok := decl.(*ast.FuncDecl)
	if !ok || fn.Recv != nil || fn.Type.Results == nil || len(fn.Type.Results.List) == 0 {
		return ""
	}
	return receiverTypeName(fn.Type.Results.List[0].Type)
}

func hasBuildConstraint(f *ast.File) bool {
	for _, group := range f.Comments {
		for _, comment := range group.List {
			if strings.HasPrefix(comment.Text, "//go:build") {
				return true
			}
		}
	}
	return false
}

func receiverTypeName(expr ast.Expr) string {
	switch expr := expr.(type) {
	case *ast.Ident:
		return expr.Name
	case *ast.StarExpr:
		return receiverTypeName(expr.X)
	case *ast.IndexExpr:
		return receiverTypeName(expr.X)
	case *ast.IndexListExpr:
		return receiverTypeName(expr.X)
	default:
		return ""
	}
}

func cleanPath(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	return filepath.Clean(abs)
}

// rel renders path relative to the working directory when it is a subpath,
// leaving it absolute otherwise.
func rel(path string) string {
	wd, err := filepath.Abs(".")
	if err != nil {
		return path
	}
	r, err := filepath.Rel(wd, path)
	if err != nil || strings.HasPrefix(r, ".."+string(filepath.Separator)) || r == ".." {
		return path
	}
	return r
}
