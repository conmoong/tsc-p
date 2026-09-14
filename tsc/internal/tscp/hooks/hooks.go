// Package hooks defines the compiled-in emit extension points for tsc-p.
//
// This package is a tsc-p addition and is not part of upstream
// microsoft/TypeScript's tsc/ native compiler. It is deliberately not a generic third-party
// plugin system: there is no dynamic loading, no configuration-file plugin
// references, and no public API surface. Plugins are first-party Go
// packages under internal/tscp compiled directly into the tsc-p binary and
// wired up explicitly by the host.
package hooks

import (
	"github.com/microsoft/TypeScript/tsc/internal/ast"
	"github.com/microsoft/TypeScript/tsc/internal/printer"
	"github.com/microsoft/TypeScript/tsc/internal/transformers"
	"github.com/microsoft/TypeScript/tsc/internal/tspath"
)

// EmitPlugin contributes transformers to the two tsc-p emit hook points.
// Implementations are shared across concurrently emitted files, so they
// must be stateless or internally synchronised; per-file state belongs in
// the transformers they return.
//
// The host passed to each factory is the emit host for the current emit; a
// plugin that needs capabilities beyond printer.EmitHost (for example
// module-resolution lookups) may type-assert it for a narrower optional
// interface and opt out (return nil) when the host does not provide them.
type EmitPlugin interface {
	// Name identifies the plugin in diagnostics and documentation.
	Name() string
	// ScriptTransformer returns a transformer to run in the JavaScript
	// output pipeline, after import elision and before module lowering.
	// It is called once per emitted file; return nil to opt out.
	ScriptTransformer(emitContext *printer.EmitContext, host printer.EmitHost) *transformers.Transformer
	// DeclarationTransformer returns a transformer to run in the
	// declaration (.d.ts) output pipeline, after the declaration
	// transformer has constructed the declaration AST and before the
	// declaration printer writes the file. It is called once per emitted
	// file; return nil to opt out.
	DeclarationTransformer(emitContext *printer.EmitContext, host printer.EmitHost) *transformers.Transformer
}

// FileDiagnosticsProvider is optionally implemented by emit plugins whose
// transformers can fail the build (invalid plugin configuration, source
// constructs the plugin defines as errors). After a file's transformers
// have run, the emitter drains the diagnostics the plugin recorded for
// that file — plus any pending configuration diagnostics — into the emit
// result, which fails the compilation like any other emit diagnostic.
// Implementations must be safe for concurrent use across files.
type FileDiagnosticsProvider interface {
	TakeDiagnostics(path tspath.Path) []*ast.Diagnostic
}

// Provider is optionally implemented by a compiler host to supply emit
// plugins. Hosts that do not implement it (including all upstream hosts)
// emit exactly as upstream does; the hook points then contribute no
// transformers and add no overhead.
type Provider interface {
	TSCPEmitPlugins() []EmitPlugin
}

// PluginsFromHost returns the emit plugins provided by host, in the order
// they should run, or nil if host does not provide any.
func PluginsFromHost(host any) []EmitPlugin {
	if provider, ok := host.(Provider); ok {
		return provider.TSCPEmitPlugins()
	}
	return nil
}
