// Package pathrewrite provides the tsc-p module-specifier rewrite plugin.
//
// This package is a tsc-p addition and is not part of upstream
// microsoft/TypeScript's tsc/ native compiler. It rewrites module specifiers during emit so
// that compiled output refers to real emitted files:
//
//   - specifiers that resolve through tsconfig path aliases (for example
//     "@app/util") become relative paths to the resolved file, and
//   - the emitted extension is derived from the resolved file:
//     .mts/.d.mts -> .mjs, .cts/.d.cts -> .cjs, .ts/.tsx/.d.ts -> .js,
//     while .json (and already-emitted .js/.mjs/.cjs) are kept as they are.
//
// Only specifiers that resolve to files inside the project are rewritten;
// bare package imports resolving into node_modules, unresolved specifiers,
// and non-literal specifiers are always left untouched. A rewrite that
// would reproduce the original text exactly leaves the node unchanged, so
// projects that already write relative, fully-extensioned imports emit
// byte-identically to upstream.
//
// The plugin participates in emit through the hook points defined in
// internal/tscp/hooks. In the tsc-p binary it is activated and configured
// per project by a compilerOptions.plugins entry named PluginName (see
// config.go and internal/tscp/defaults); when inactive, emit behaves
// exactly as upstream.
package pathrewrite

import (
	"github.com/microsoft/TypeScript/tsc/internal/ast"
	"github.com/microsoft/TypeScript/tsc/internal/printer"
	"github.com/microsoft/TypeScript/tsc/internal/transformers"
	"github.com/microsoft/TypeScript/tsc/internal/tscp/hooks"
)

// EmitKind identifies which emit pipeline a rewrite is running in.
type EmitKind int

const (
	// EmitKindScript is the JavaScript output pipeline. The rewrite runs
	// after import elision and before module lowering, so a single rewrite
	// flows into both ESM and CommonJS output.
	EmitKindScript EmitKind = iota
	// EmitKindDeclaration is the declaration (.d.ts) output pipeline. The
	// rewrite runs after the declaration transformer has constructed the
	// declaration AST and before the declaration printer writes the file.
	EmitKindDeclaration
)

// SpecifierSyntax identifies the syntactic construct that contains the
// module specifier being offered to the rewriter.
type SpecifierSyntax int

const (
	// SyntaxImportDeclaration is `import ... from "specifier"`.
	SyntaxImportDeclaration SpecifierSyntax = iota
	// SyntaxSideEffectImport is `import "specifier"` with no import clause.
	SyntaxSideEffectImport
	// SyntaxExportDeclaration is `export ... from "specifier"`.
	SyntaxExportDeclaration
	// SyntaxImportCall is a dynamic `import("specifier")` expression.
	SyntaxImportCall
	// SyntaxImportEquals is `import x = require("specifier")`.
	SyntaxImportEquals
	// SyntaxImportType is an `import("specifier").T` type reference.
	SyntaxImportType
	// SyntaxRequireCall is a call expression of the form
	// `require("specifier")`. The visitor performs no semantic analysis:
	// a local binding named `require` produces this syntax kind too, so a
	// rewriter must not assume the call is a Node.js require.
	SyntaxRequireCall
)

// RewriteContext carries the location information supplied to a rewriter
// alongside each candidate specifier.
type RewriteContext struct {
	// EmitKind is the pipeline (script or declaration) being emitted.
	EmitKind EmitKind
	// Syntax is the syntactic construct containing the specifier.
	Syntax SpecifierSyntax
	// SourceFile is the source file being emitted.
	SourceFile *ast.SourceFile
	// SpecifierNode is the specifier's string-literal node — the original
	// parse-tree node when one exists, otherwise the (possibly synthesised)
	// node being emitted. It may be nil. Rewriters that resolve modules
	// through the compiler must tolerate synthesised nodes with incomplete
	// parent chains.
	SpecifierNode *ast.Node
}

// ModuleSpecifierRewriter decides whether a module specifier should be
// replaced during emit. Implementations must be safe for concurrent use:
// emit may run for many files in parallel and the same rewriter instance is
// shared between them, so implementations must not rely on mutable state.
type ModuleSpecifierRewriter interface {
	// RewriteModuleSpecifier returns the replacement specifier text and
	// whether a change should be applied. Returning changed == false (or a
	// replacement equal to the input) leaves the node untouched.
	RewriteModuleSpecifier(context RewriteContext, specifier string) (replacement string, changed bool)
}

// Identity is a rewriter that never changes a specifier.
type Identity struct{}

var _ ModuleSpecifierRewriter = Identity{}

// RewriteModuleSpecifier implements ModuleSpecifierRewriter.
func (Identity) RewriteModuleSpecifier(context RewriteContext, specifier string) (replacement string, changed bool) {
	return specifier, false
}

// plugin adapts a rewriter factory to the tsc-p emit hook points. The
// factory runs once per transformer construction (per emitted file) and may
// return nil to opt out for hosts that lack required capabilities.
type plugin struct {
	factory      func(host printer.EmitHost) ModuleSpecifierRewriter
	declarations bool
}

var _ hooks.EmitPlugin = (*plugin)(nil)

// NewPlugin wraps a fixed rewriter as an emit plugin that rewrites module
// specifiers in both the script and declaration pipelines. The rewriter
// must be non-nil and safe for concurrent use.
func NewPlugin(rewriter ModuleSpecifierRewriter) hooks.EmitPlugin {
	if rewriter == nil {
		panic("pathrewrite.NewPlugin requires a rewriter")
	}
	return &plugin{
		factory:      func(printer.EmitHost) ModuleSpecifierRewriter { return rewriter },
		declarations: true,
	}
}

// NewHostPlugin returns the production plugin: module specifiers are
// rewritten using the compiler's own resolution results, obtained from the
// emit host, according to options (nil = defaults). On hosts that cannot
// supply resolution data the plugin opts out entirely and emit is
// unchanged.
func NewHostPlugin(options *Options) hooks.EmitPlugin {
	if options == nil {
		options = defaultOptions()
	}
	return &plugin{
		factory: func(host printer.EmitHost) ModuleSpecifierRewriter {
			return NewHostRewriter(host, options)
		},
		declarations: options.Declarations,
	}
}

func (p *plugin) Name() string {
	return "pathrewrite"
}

func (p *plugin) ScriptTransformer(emitContext *printer.EmitContext, host printer.EmitHost) *transformers.Transformer {
	if rewriter := p.factory(host); rewriter != nil {
		return NewTransformer(emitContext, rewriter, EmitKindScript)
	}
	return nil
}

func (p *plugin) DeclarationTransformer(emitContext *printer.EmitContext, host printer.EmitHost) *transformers.Transformer {
	if !p.declarations {
		return nil
	}
	if rewriter := p.factory(host); rewriter != nil {
		return NewTransformer(emitContext, rewriter, EmitKindDeclaration)
	}
	return nil
}
