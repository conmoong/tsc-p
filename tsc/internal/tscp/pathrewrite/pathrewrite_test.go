package pathrewrite_test

import (
	"testing"

	"github.com/microsoft/TypeScript/tsc/internal/printer"
	"github.com/microsoft/TypeScript/tsc/internal/testutil/emittestutil"
	"github.com/microsoft/TypeScript/tsc/internal/testutil/parsetestutil"
	"github.com/microsoft/TypeScript/tsc/internal/tscp/pathrewrite"
	"gotest.tools/v3/assert"
)

// recordingRewriter records every specifier offered to it and applies a
// fixed replacement function.
type recordingRewriter struct {
	rewrite func(context pathrewrite.RewriteContext, specifier string) (string, bool)
	calls   []recordedCall
}

type recordedCall struct {
	context   pathrewrite.RewriteContext
	specifier string
}

func (r *recordingRewriter) RewriteModuleSpecifier(context pathrewrite.RewriteContext, specifier string) (string, bool) {
	r.calls = append(r.calls, recordedCall{context, specifier})
	if r.rewrite == nil {
		return specifier, false
	}
	return r.rewrite(context, specifier)
}

// prefixAll rewrites every offered specifier to "rw:" + specifier.
func prefixAll(context pathrewrite.RewriteContext, specifier string) (string, bool) {
	return "rw:" + specifier, true
}

// allSyntaxesSource contains every module-specifier syntax the framework
// supports.
const allSyntaxesSource = `import d from "m1";
import "m2";
export { x } from "m3";
export * from "m4";
import e = require("m5");
const p = import("m6");
type T = import("m7").T;
const r = require("m8");
`

func TestIdentityReturnsSameSourceFile(t *testing.T) {
	t.Parallel()

	file := parsetestutil.ParseTypeScript(allSyntaxesSource, false /*jsx*/)
	parsetestutil.CheckDiagnostics(t, file)
	emitContext := printer.NewEmitContext()
	result := pathrewrite.NewTransformer(emitContext, pathrewrite.Identity{}, pathrewrite.EmitKindScript).TransformSourceFile(file)

	// An identity rewrite must return the identical AST, not an equivalent copy.
	assert.Assert(t, result == file, "identity rewrite must return the same *ast.SourceFile")
}

func TestUnchangedRewriterReturnsSameSourceFile(t *testing.T) {
	t.Parallel()

	file := parsetestutil.ParseTypeScript(allSyntaxesSource, false /*jsx*/)
	parsetestutil.CheckDiagnostics(t, file)
	emitContext := printer.NewEmitContext()
	// A rewriter that reports changed == true but returns identical text
	// must also leave the AST untouched.
	rewriter := &recordingRewriter{rewrite: func(context pathrewrite.RewriteContext, specifier string) (string, bool) {
		return specifier, true
	}}
	result := pathrewrite.NewTransformer(emitContext, rewriter, pathrewrite.EmitKindScript).TransformSourceFile(file)

	assert.Assert(t, result == file, "no-op rewrite must return the same *ast.SourceFile")
	assert.Assert(t, len(rewriter.calls) == 8, "expected all 8 specifiers to be offered, got %d", len(rewriter.calls))
}

func TestRewritesEverySpecifierSyntax(t *testing.T) {
	t.Parallel()

	file := parsetestutil.ParseTypeScript(allSyntaxesSource, false /*jsx*/)
	parsetestutil.CheckDiagnostics(t, file)
	emitContext := printer.NewEmitContext()
	rewriter := &recordingRewriter{rewrite: prefixAll}
	result := pathrewrite.NewTransformer(emitContext, rewriter, pathrewrite.EmitKindScript).TransformSourceFile(file)

	expected := `import d from "rw:m1";
import "rw:m2";
export { x } from "rw:m3";
export * from "rw:m4";
import e = require("rw:m5");
const p = import("rw:m6");
type T = import("rw:m7").T;
const r = require("rw:m8");`
	emittestutil.CheckEmit(t, emitContext, result, expected)

	// Verify the syntax discriminant reported for each site.
	expectedSyntaxes := map[string]pathrewrite.SpecifierSyntax{
		"m1": pathrewrite.SyntaxImportDeclaration,
		"m2": pathrewrite.SyntaxSideEffectImport,
		"m3": pathrewrite.SyntaxExportDeclaration,
		"m4": pathrewrite.SyntaxExportDeclaration,
		"m5": pathrewrite.SyntaxImportEquals,
		"m6": pathrewrite.SyntaxImportCall,
		"m7": pathrewrite.SyntaxImportType,
		"m8": pathrewrite.SyntaxRequireCall,
	}
	assert.Assert(t, len(rewriter.calls) == len(expectedSyntaxes), "expected %d rewrites, got %d", len(expectedSyntaxes), len(rewriter.calls))
	for _, call := range rewriter.calls {
		assert.Equal(t, expectedSyntaxes[call.specifier], call.context.Syntax, "syntax for %q", call.specifier)
		assert.Equal(t, pathrewrite.EmitKindScript, call.context.EmitKind)
		assert.Assert(t, call.context.SourceFile == file, "source file for %q", call.specifier)
		assert.Assert(t, call.context.SpecifierNode != nil, "specifier node for %q", call.specifier)
		assert.Equal(t, call.specifier, call.context.SpecifierNode.Text(), "specifier node text for %q", call.specifier)
	}
}

func TestEmitKindIsReportedToRewriter(t *testing.T) {
	t.Parallel()

	file := parsetestutil.ParseTypeScript(`import d from "m1";`, false /*jsx*/)
	parsetestutil.CheckDiagnostics(t, file)
	emitContext := printer.NewEmitContext()
	rewriter := &recordingRewriter{}
	pathrewrite.NewTransformer(emitContext, rewriter, pathrewrite.EmitKindDeclaration).TransformSourceFile(file)

	assert.Assert(t, len(rewriter.calls) == 1)
	assert.Equal(t, pathrewrite.EmitKindDeclaration, rewriter.calls[0].context.EmitKind)
}

func TestRequireCallIsExposedButNotAssumed(t *testing.T) {
	t.Parallel()

	// A local function named require must still be offered to the rewriter
	// as SyntaxRequireCall; declining leaves it untouched.
	source := `function require(m: string): unknown { return m; }
const a = require("local");`
	file := parsetestutil.ParseTypeScript(source, false /*jsx*/)
	parsetestutil.CheckDiagnostics(t, file)
	emitContext := printer.NewEmitContext()
	rewriter := &recordingRewriter{}
	result := pathrewrite.NewTransformer(emitContext, rewriter, pathrewrite.EmitKindScript).TransformSourceFile(file)

	assert.Assert(t, result == file)
	assert.Assert(t, len(rewriter.calls) == 1)
	assert.Equal(t, pathrewrite.SyntaxRequireCall, rewriter.calls[0].context.Syntax)
	assert.Equal(t, "local", rewriter.calls[0].specifier)
}

func TestNonLiteralSpecifiersAreNotOffered(t *testing.T) {
	t.Parallel()

	// Computed dynamic imports, template arguments, and multi-argument
	// require calls carry no rewritable string literal.
	source := `const name = "m";
const a = import(name);
const b = require(name);
const c = require("x", "y");
const d = import(` + "`./${name}`" + `);`
	file := parsetestutil.ParseTypeScript(source, false /*jsx*/)
	parsetestutil.CheckDiagnostics(t, file)
	emitContext := printer.NewEmitContext()
	rewriter := &recordingRewriter{rewrite: prefixAll}
	result := pathrewrite.NewTransformer(emitContext, rewriter, pathrewrite.EmitKindScript).TransformSourceFile(file)

	assert.Assert(t, result == file)
	assert.Assert(t, len(rewriter.calls) == 0, "expected no rewrite offers, got %d", len(rewriter.calls))
}

func TestNestedImportCallsAndImportTypes(t *testing.T) {
	t.Parallel()

	source := `const a = import("outer").then(() => import("inner"));
type T = import("t1").A<import("t2").B>;`
	file := parsetestutil.ParseTypeScript(source, false /*jsx*/)
	parsetestutil.CheckDiagnostics(t, file)
	emitContext := printer.NewEmitContext()
	rewriter := &recordingRewriter{rewrite: prefixAll}
	result := pathrewrite.NewTransformer(emitContext, rewriter, pathrewrite.EmitKindScript).TransformSourceFile(file)

	expected := `const a = import("rw:outer").then(() => import("rw:inner"));
type T = import("rw:t1").A<import("rw:t2").B>;`
	emittestutil.CheckEmit(t, emitContext, result, expected)
	assert.Assert(t, len(rewriter.calls) == 4, "expected 4 rewrites, got %d", len(rewriter.calls))
}

func TestModuleDeclarationNamesAreNotRewritten(t *testing.T) {
	t.Parallel()

	// An ambient module declaration's name is not an import site, but the
	// imports inside its body are.
	source := `declare module "ambient" {
    import d from "m1";
}`
	file := parsetestutil.ParseTypeScript(source, false /*jsx*/)
	parsetestutil.CheckDiagnostics(t, file)
	emitContext := printer.NewEmitContext()
	rewriter := &recordingRewriter{rewrite: prefixAll}
	result := pathrewrite.NewTransformer(emitContext, rewriter, pathrewrite.EmitKindScript).TransformSourceFile(file)

	expected := `declare module "ambient" {
    import d from "rw:m1";
}`
	emittestutil.CheckEmit(t, emitContext, result, expected)
	assert.Assert(t, len(rewriter.calls) == 1)
	assert.Equal(t, "m1", rewriter.calls[0].specifier)
}

func TestCommentsArePreservedOnRewrite(t *testing.T) {
	t.Parallel()

	source := `// leading comment
import d from "m1";`
	file := parsetestutil.ParseTypeScript(source, false /*jsx*/)
	parsetestutil.CheckDiagnostics(t, file)
	emitContext := printer.NewEmitContext()
	rewriter := &recordingRewriter{rewrite: prefixAll}
	result := pathrewrite.NewTransformer(emitContext, rewriter, pathrewrite.EmitKindScript).TransformSourceFile(file)

	expected := `// leading comment
import d from "rw:m1";`
	emittestutil.CheckEmit(t, emitContext, result, expected)
}

func TestPluginProvidesBothPipelines(t *testing.T) {
	t.Parallel()

	plugin := pathrewrite.NewPlugin(pathrewrite.Identity{})
	assert.Equal(t, "pathrewrite", plugin.Name())

	file := parsetestutil.ParseTypeScript(`import d from "m1";`, false /*jsx*/)
	parsetestutil.CheckDiagnostics(t, file)
	emitContext := printer.NewEmitContext()

	script := plugin.ScriptTransformer(emitContext, nil /*host*/)
	assert.Assert(t, script != nil)
	assert.Assert(t, script.TransformSourceFile(file) == file)

	declaration := plugin.DeclarationTransformer(emitContext, nil /*host*/)
	assert.Assert(t, declaration != nil)
	assert.Assert(t, declaration.TransformSourceFile(file) == file)
}

func TestHostPluginOptsOutWithoutResolution(t *testing.T) {
	t.Parallel()

	// A host that cannot supply module resolution (nil here) must cause the
	// production plugin to contribute no transformers at all.
	plugin := pathrewrite.NewHostPlugin(nil)
	emitContext := printer.NewEmitContext()
	assert.Assert(t, plugin.ScriptTransformer(emitContext, nil /*host*/) == nil)
	assert.Assert(t, plugin.DeclarationTransformer(emitContext, nil /*host*/) == nil)
}
