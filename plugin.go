// Copyright 2026 Marc-Antoine Ruel. All Rights Reserved. Use of this
// source code is governed by the Apache v2 license that can be found in the
// LICENSE file.

// This file provides methodfilecheck as a golangci-lint module plugin. Point
// .custom-gcl.yml at this module, build the linter with `golangci-lint custom`,
// and enable the linter named "methodfilecheck" in .golangci.yml:
//
//	# .custom-gcl.yml
//	version: "2"
//	plugins:
//	  - module: github.com/maruel/methodfilecheck
//	    import: github.com/maruel/methodfilecheck
//	    path: ../methodfilecheck
//
//	# .golangci.yml
//	version: "2"
//	linters:
//	  enable:
//	    - methodfilecheck
//	  settings:
//	    custom:
//	      methodfilecheck:
//	        type: module

package methodfilecheck

import (
	"go/token"
	"strings"

	"github.com/golangci/plugin-module-register/register"
	"golang.org/x/tools/go/analysis"
)

func init() {
	register.Plugin("methodfilecheck", newPlugin)
}

// settings is empty; the linter has no options yet.
type settings struct{}

func newPlugin(conf any) (register.LinterPlugin, error) {
	if _, err := register.DecodeSettings[settings](conf); err != nil {
		return nil, err
	}
	return plugin{}, nil
}

type plugin struct{}

// BuildAnalyzers implements register.LinterPlugin.
func (plugin) BuildAnalyzers() ([]*analysis.Analyzer, error) {
	return []*analysis.Analyzer{
		{
			Name: "methodfilecheck",
			Doc:  "reports methods declared in a different file than their receiver type and method blocks interleaved with other declarations",
			Run:  pluginRun,
		},
	}, nil
}

// GetLoadMode implements register.LinterPlugin.
func (plugin) GetLoadMode() string {
	return register.LoadModeSyntax
}

func pluginRun(pass *analysis.Pass) (interface{}, error) {
	for _, v := range CheckSyntax(pass.Fset, pass.Files) {
		diag := analysis.Diagnostic{Pos: v.Pos, Message: v.Message, Category: "methodfilecheck"}
		// golangci-lint applies suggested fixes to the file the diagnostic was
		// reported in, so only same-file moves can be fixed through
		// `golangci-lint run --fix`; cross-file moves need `methodfilecheck -fix`.
		if v.Fix != nil && v.Fix.SrcFile == v.Fix.DstFile {
			if edits, ok := suggestedEdits(pass.Fset, v); ok {
				diag.SuggestedFixes = []analysis.SuggestedFix{
					{Message: "Reorder declarations to satisfy methodfilecheck", TextEdits: edits},
				}
			}
		}
		pass.Report(diag)
	}
	return nil, nil //nolint:nilnil // analysis.Run results are unused by golangci-lint
}

// suggestedEdits returns the TextEdit applying a same-file fix to the file
// holding the violation: the affected region is replaced such that the
// misplaced block lands at its destination, blank line padding included.
func suggestedEdits(fset *token.FileSet, v Violation) ([]analysis.TextEdit, bool) {
	start, end, replacement, ok := v.Fix.LineEdit()
	if !ok {
		return nil, false
	}
	tf := fset.File(v.Pos)
	if tf == nil || end > tf.LineCount() {
		return nil, false
	}
	editEnd := tf.Pos(tf.Size())
	if end < tf.LineCount() {
		editEnd = tf.LineStart(end + 1)
	}
	return []analysis.TextEdit{{
		Pos:     tf.LineStart(start),
		End:     editEnd,
		NewText: []byte(strings.Join(replacement, "\n") + "\n"),
	}}, true
}
