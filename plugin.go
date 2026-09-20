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
			if edit, ok := wholeFileEdit(pass.Fset, v); ok {
				diag.SuggestedFixes = []analysis.SuggestedFix{
					{Message: "Reorder declarations to satisfy methodfilecheck", TextEdits: []analysis.TextEdit{edit}},
				}
			}
		}
		pass.Report(diag)
	}
	return nil, nil //nolint:nilnil // analysis.Run results are unused by golangci-lint
}

// wholeFileEdit returns a TextEdit replacing the file holding the violation
// with its gofmt-clean, fixed contents.
func wholeFileEdit(fset *token.FileSet, v Violation) (analysis.TextEdit, bool) {
	contents, err := v.Fix.Contents()
	if err != nil {
		return analysis.TextEdit{}, false
	}
	tf := fset.File(v.Pos)
	if tf == nil {
		return analysis.TextEdit{}, false
	}
	content, ok := contents[v.Fix.SrcFile]
	if !ok {
		return analysis.TextEdit{}, false
	}
	return analysis.TextEdit{Pos: tf.Pos(0), End: tf.Pos(tf.Size()), NewText: content}, true
}
