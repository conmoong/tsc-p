package tsc

// tsc-p addition: a new test file in an upstream package, never an edit to
// an upstream test, so upstream merges cannot conflict with it.

import (
	"testing"

	"github.com/microsoft/TypeScript/tsc/internal/ast"
	"github.com/microsoft/TypeScript/tsc/internal/diagnostics"
	"gotest.tools/v3/assert"
)

func diagnosticOf(category diagnostics.Category) *ast.Diagnostic {
	d := ast.NewCompilerDiagnostic(diagnostics.Tscp_graph_0, "finding")
	d.SetCategory(category)
	return d
}

// TestHasErrorDiagnosticsIgnoresNonErrors pins that only error-category
// diagnostics decide the exit status, so plugin warnings are reported
// without failing the build — matching upstream's own error summary.
func TestHasErrorDiagnosticsIgnoresNonErrors(t *testing.T) {
	t.Parallel()

	assert.Assert(t, !hasErrorDiagnostics(nil))
	assert.Assert(t, !hasErrorDiagnostics([]*ast.Diagnostic{
		diagnosticOf(diagnostics.CategoryWarning),
		diagnosticOf(diagnostics.CategoryMessage),
		diagnosticOf(diagnostics.CategorySuggestion),
	}), "a build with only non-error diagnostics must succeed")
	assert.Assert(t, hasErrorDiagnostics([]*ast.Diagnostic{
		diagnosticOf(diagnostics.CategoryWarning),
		diagnosticOf(diagnostics.CategoryError),
	}), "one error among warnings must still fail the build")
}
