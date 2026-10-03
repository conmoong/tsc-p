// Package graph implements @conmoong/graph, tsc-p's module-graph fact
// gathering and checking plugin: import cycles, layer/boundary rules,
// phantom npm dependencies, dev-dependency leaks, and unused files/deps —
// deliberately never style/correctness lint (that stays tsgolint/oxlint's
// job; "linters check files, tsc-p checks the graph").
//
// The plugin does exactly one of two things, chosen by its "emit" config:
//
//   - emit truthy: writes a fact-only JSON artifact (no rule config, no
//     severities — files, exports, resolved edges, and the specific named
//     bindings used, useful as a future bundler tree-shaking input as much
//     as for checks) next to the tsconfig, and performs NO rule evaluation
//     itself even if "rules" is also configured — evaluation is deferred
//     to the separate @conmoong/graph-validate tool, which can combine
//     this project's own rules with a workspace root's when run across a
//     tsconfig "references" tree. Both being set is not an error: an info
//     note is printed once explaining the deferral.
//   - only "rules" set (no emit): tsc-p evaluates them itself as ordinary
//     build diagnostics, including unused — this project's own graph is
//     known-complete by the time the last file's TakeDiagnostics call
//     fires (see that method's doc comment), so no separate tool is
//     needed for a single-project build.
//
// This plugin never modifies emitted output at all — unlike every other
// tsc-p plugin, its script "transform" is purely a read-only fact-gathering
// walk (see transformer.go), so byte-parity with upstream holds even when
// the plugin is active. It is registered LAST among script transformers
// (see internal/tscp/defaults), so gathered facts reflect the FINAL,
// post-other-plugin-transform module surface.
package graph

import (
	"fmt"
	"os"
	"sync"

	"github.com/microsoft/TypeScript/tsc/internal/ast"
	"github.com/microsoft/TypeScript/tsc/internal/core"
	"github.com/microsoft/TypeScript/tsc/internal/diagnostics"
	"github.com/microsoft/TypeScript/tsc/internal/printer"
	"github.com/microsoft/TypeScript/tsc/internal/transformers"
	"github.com/microsoft/TypeScript/tsc/internal/tscp/hooks"
	"github.com/microsoft/TypeScript/tsc/internal/tspath"
)

// Plugin is the @conmoong/graph emit plugin, shared across every file of
// one compilation.
type Plugin struct {
	options *Options
	acc     *accumulator

	mu                  sync.Mutex
	reported            map[string]bool
	notedEmitRulesInfo  bool
	notedUnusedDeferral bool
	fileDiagnostics     map[tspath.Path][]*ast.Diagnostic

	totalFilesOnce sync.Once
	totalFiles     int
	seen           map[tspath.Path]bool
	graphComplete  bool
}

var (
	_ hooks.EmitPlugin              = (*Plugin)(nil)
	_ hooks.FileDiagnosticsProvider = (*Plugin)(nil)
)

// NewPlugin creates the plugin for one compilation's options.
func NewPlugin(options *Options) *Plugin {
	if options == nil {
		options = &Options{}
	}
	return &Plugin{
		options:  options,
		acc:      newAccumulator(),
		reported: make(map[string]bool),
		seen:     make(map[tspath.Path]bool),
	}
}

func (p *Plugin) Name() string { return PluginName }

func (p *Plugin) ScriptTransformer(emitContext *printer.EmitContext, host printer.EmitHost) *transformers.Transformer {
	// host is the same for every file of this compilation; capture the
	// total non-declaration file count once, used by TakeDiagnostics to
	// detect when every file's own edge-walk is done (see its doc comment).
	p.totalFilesOnce.Do(func() {
		p.totalFiles = countEmittableFiles(host.SourceFiles())
	})
	return NewTransformer(emitContext, host, p.acc, p.options.ConfigDir)
}

// countEmittableFiles counts the non-declaration files in a full program's
// SourceFiles() — a conservative (safe-to-overcount, unsafe-to-undercount)
// proxy for exactly which files this plugin's ScriptTransformer will run
// against: overcounting only delays "graph is complete" detection (the
// unused check simply never fires for that build, no worse than today's
// baseline of never evaluating it at all); undercounting could trigger
// unused evaluation before every file's edges are actually in, risking a
// false positive. Declaration (.d.ts) input files are excluded because
// they are not script-transformed.
func countEmittableFiles(sourceFiles []*ast.SourceFile) int {
	count := 0
	for _, file := range sourceFiles {
		if !file.IsDeclarationFile {
			count++
		}
	}
	return count
}

func (p *Plugin) DeclarationTransformer(emitContext *printer.EmitContext, host printer.EmitHost) *transformers.Transformer {
	// The module graph is fully described by the script pipeline (imports,
	// exports, requires); nothing in declaration output adds new edges.
	return nil
}

// TakeDiagnostics runs after each file's script transform (see
// emitter.go): it is the natural per-file point at which this plugin
// either (a) refreshes the emitted graph.json with everything
// accumulated so far — safe to do redundantly, since only the state after
// the LAST file's write matters and every intermediate write is itself a
// perfectly well-formed graph — or (b) evaluates rules against the
// current snapshot and returns any NEWLY found diagnostics not already
// reported.
//
// (b) evaluates EvaluateIncremental (excludes unused — see its doc
// comment) on every call, since that subset is monotonic and safe against
// a partial graph. It additionally tracks, via "seen", how many distinct
// files have had TakeDiagnostics called at least once: since this method
// only fires after a file's own script transform (and therefore its whole
// edge-walk) has finished, len(seen) reaching totalFiles (captured once
// from the full program in ScriptTransformer) proves every file's edges
// are now in the accumulator — at that point, and only once, this method
// switches to the full Evaluate (which includes unused) for the
// remaining call, so unused gets checked without ever needing a real
// whole-program "done" hook. If totalFiles is ever over-counted (see
// countEmittableFiles), completion is simply never detected and unused
// silently never fires for that build — the safe failure direction,
// consistent with graph-validate remaining the only unconditionally
// correct place to run this check.
func (p *Plugin) TakeDiagnostics(path tspath.Path) []*ast.Diagnostic {
	p.mu.Lock()
	fileDiagnostics := p.fileDiagnostics[path]
	delete(p.fileDiagnostics, path)
	p.mu.Unlock()

	snapshot := p.acc.snapshot(p.options.PackageName, p.options.EmitPath)

	if p.options.EmitEnabled {
		if len(p.options.Rules) > 0 {
			p.noteEmitRulesDeferral(path)
		}
		if anyUnusedConfigured(p.options.Rules) {
			p.noteUnusedRequiresGraphValidate(path)
		}
		if err := writeGraph(p.options.EmitPath, snapshot); err != nil {
			return append(fileDiagnostics, p.diagnostic(nil, "failed to write %s: %v", p.options.EmitPath, err))
		}
		return fileDiagnostics
	}

	if len(p.options.Rules) == 0 {
		return fileDiagnostics
	}

	p.mu.Lock()
	p.seen[path] = true
	complete := !p.graphComplete && len(p.seen) >= p.totalFiles
	if complete {
		p.graphComplete = true
	}
	p.mu.Unlock()

	var findings []Finding
	if complete {
		findings = Evaluate(snapshot, p.options.Rules, p.options.Dependencies, p.options.DevDependencies)
	} else {
		findings = EvaluateIncremental(snapshot, p.options.Rules, p.options.Dependencies, p.options.DevDependencies)
	}
	for _, finding := range findings {
		key := finding.Check + "|" + finding.File + "|" + finding.Message
		p.mu.Lock()
		alreadyReported := p.reported[key]
		p.reported[key] = true
		p.mu.Unlock()
		if alreadyReported || finding.Severity == SeverityAllow {
			continue
		}
		sourceFile := p.acc.sourceFileFor(finding.File)
		category := diagnostics.CategoryWarning
		if finding.Severity == SeverityError {
			category = diagnostics.CategoryError
		}
		fileDiagnostics = append(fileDiagnostics, p.diagnosticWithCategory(sourceFile, category, "[%s] %s", finding.Check, finding.Message))
	}
	return fileDiagnostics
}

func (p *Plugin) noteEmitRulesDeferral(path tspath.Path) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.notedEmitRulesInfo {
		return
	}
	p.notedEmitRulesInfo = true
	sourceFile := p.acc.sourceFileFor(string(path))
	diagnostic := p.diagnosticWithCategory(sourceFile, diagnostics.CategoryMessage,
		"@conmoong/graph: \"emit\" is enabled, so the graph is written but \"rules\" are not evaluated here — run @conmoong/graph-validate against this tsconfig to apply them")
	if p.fileDiagnostics == nil {
		p.fileDiagnostics = make(map[tspath.Path][]*ast.Diagnostic)
	}
	p.fileDiagnostics[path] = append(p.fileDiagnostics[path], diagnostic)
}

// noteUnusedRequiresGraphValidate warns once, specifically, when "unused"
// is configured while "emit" is enabled: unlike cycle/phantomImport/
// devLeak/importRules (which tsc-p itself CAN check, just not in this
// mode), "unused" is NEVER evaluated by tsc-p directly, in any
// configuration — only @conmoong/graph-validate ever runs it, since it
// requires a known-complete graph.
func (p *Plugin) noteUnusedRequiresGraphValidate(path tspath.Path) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.notedUnusedDeferral {
		return
	}
	p.notedUnusedDeferral = true
	sourceFile := p.acc.sourceFileFor(string(path))
	diagnostic := p.diagnosticWithCategory(sourceFile, diagnostics.CategoryMessage,
		"@conmoong/graph: \"unused\" is configured but is never evaluated by tsc-p itself — run @conmoong/graph-validate against this tsconfig to enforce it")
	if p.fileDiagnostics == nil {
		p.fileDiagnostics = make(map[tspath.Path][]*ast.Diagnostic)
	}
	p.fileDiagnostics[path] = append(p.fileDiagnostics[path], diagnostic)
}

func anyUnusedConfigured(rules []Rule) bool {
	for _, rule := range rules {
		if rule.Unused != nil {
			return true
		}
	}
	return false
}

func (p *Plugin) diagnostic(sourceFile *ast.SourceFile, format string, args ...any) *ast.Diagnostic {
	return p.diagnosticWithCategory(sourceFile, diagnostics.CategoryError, format, args...)
}

func (p *Plugin) diagnosticWithCategory(sourceFile *ast.SourceFile, category diagnostics.Category, format string, args ...any) *ast.Diagnostic {
	message := fmt.Sprintf(format, args...)
	var diagnostic *ast.Diagnostic
	if sourceFile == nil {
		diagnostic = ast.NewCompilerDiagnostic(diagnostics.Tscp_graph_0, message)
	} else {
		diagnostic = ast.NewDiagnostic(sourceFile, core.NewTextRange(0, 0), diagnostics.Tscp_graph_0, message)
	}
	diagnostic.SetCategory(category)
	return diagnostic
}

func writeGraph(path string, g *Graph) error {
	data, err := g.MarshalJSON()
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
