// Package paris implements @conmoong/paris, tsc-p's conditional-compilation
// and value-substitution emit plugin ("Avec des si on mettrait Paris en
// bouteille"). Source code imports the ordinary @conmoong/paris runtime
// package and calls ifDef/ifEq/.../value; when the plugin is configured,
// those calls are frozen at build time: condition bodies survive as plain
// block statements or disappear, value(name) calls become literals, and the
// runtime import is removed. Anything the design defines as misuse is a
// build-failing diagnostic — this plugin is deliberately explicit-and-loud,
// never a silent fallback.
package paris

import (
	"sync"

	"github.com/microsoft/TypeScript/tsc/internal/ast"
	"github.com/microsoft/TypeScript/tsc/internal/diagnostics"
	"github.com/microsoft/TypeScript/tsc/internal/printer"
	"github.com/microsoft/TypeScript/tsc/internal/scanner"
	"github.com/microsoft/TypeScript/tsc/internal/transformers"
	"github.com/microsoft/TypeScript/tsc/internal/tscp/hooks"
	"github.com/microsoft/TypeScript/tsc/internal/tspath"
)

// Plugin is the @conmoong/paris emit plugin. It is shared across
// concurrently emitted files; per-file diagnostics are recorded under the
// file's path and drained by the emitter through
// hooks.FileDiagnosticsProvider.
type Plugin struct {
	options *Options

	mu             sync.Mutex
	diagnostics    map[tspath.Path][]*ast.Diagnostic
	configReported bool
}

var (
	_ hooks.EmitPlugin              = (*Plugin)(nil)
	_ hooks.FileDiagnosticsProvider = (*Plugin)(nil)
)

// NewPlugin creates the plugin for one compilation's options.
func NewPlugin(options *Options) *Plugin {
	if options == nil {
		options = &Options{Values: map[string]Value{}}
	}
	return &Plugin{options: options}
}

func (p *Plugin) Name() string { return PluginName }

func (p *Plugin) ScriptTransformer(emitContext *printer.EmitContext, host printer.EmitHost) *transformers.Transformer {
	return NewTransformer(emitContext, p)
}

func (p *Plugin) DeclarationTransformer(emitContext *printer.EmitContext, host printer.EmitHost) *transformers.Transformer {
	// paris never appears in type positions, so declaration output needs no
	// transform; the runtime import is dropped from declarations by
	// upstream's own unused-import handling.
	return nil
}

func (p *Plugin) addDiagnostic(file *ast.SourceFile, node *ast.Node, message string) {
	diagnostic := ast.NewDiagnostic(file, scanner.GetErrorRangeForNode(file, node), diagnostics.Tscp_paris_0, message)
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.diagnostics == nil {
		p.diagnostics = make(map[tspath.Path][]*ast.Diagnostic)
	}
	p.diagnostics[file.Path()] = append(p.diagnostics[file.Path()], diagnostic)
}

// TakeDiagnostics drains the diagnostics recorded for one file. The first
// drain also reports configuration errors (which belong to no file).
func (p *Plugin) TakeDiagnostics(path tspath.Path) []*ast.Diagnostic {
	p.mu.Lock()
	defer p.mu.Unlock()
	var result []*ast.Diagnostic
	if !p.configReported {
		p.configReported = true
		for _, message := range p.options.ConfigErrors {
			result = append(result, ast.NewCompilerDiagnostic(diagnostics.Tscp_paris_0, message))
		}
	}
	if fileDiagnostics, ok := p.diagnostics[path]; ok {
		delete(p.diagnostics, path)
		result = append(result, fileDiagnostics...)
	}
	return result
}
