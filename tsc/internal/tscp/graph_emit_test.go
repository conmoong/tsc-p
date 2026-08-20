// Integration tests for the @conmoong/graph plugin through the real
// compiler emit pipeline: fact gathering into the emitted JSON artifact,
// and inline rule diagnostics (cycle/phantomImport/devLeak/importRules)
// when "rules" is configured without "emit".

package tscp_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/microsoft/TypeScript/tsc/internal/ast"
	"github.com/microsoft/TypeScript/tsc/internal/bundled"
	"github.com/microsoft/TypeScript/tsc/internal/compiler"
	"github.com/microsoft/TypeScript/tsc/internal/core"
	"github.com/microsoft/TypeScript/tsc/internal/tscp/graph"
	"github.com/microsoft/TypeScript/tsc/internal/tscp/hooks"
	"github.com/microsoft/TypeScript/tsc/internal/tsoptions"
	"github.com/microsoft/TypeScript/tsc/internal/vfs/vfstest"
	"gotest.tools/v3/assert"
)

func graphPlugin(t *testing.T, config map[string]any) hooks.EmitPlugin {
	t.Helper()
	full := map[string]any{"name": graph.PluginName}
	for k, v := range config {
		full[k] = v
	}
	raw := map[string]any{
		"compilerOptions": map[string]any{
			"plugins": []any{full},
		},
	}
	options := graph.OptionsFromConfig(raw, "/src", "/src/tsconfig.json")
	assert.Assert(t, options != nil)
	return graph.NewPlugin(options)
}

// emitGraphFixture compiles sources with the graph plugin and returns
// outputs and emit diagnostics, without asserting the diagnostics are
// empty — unlike emitFixture, since graph's rules mode deliberately
// produces diagnostics for its findings.
func emitGraphFixture(t *testing.T, sources map[string]string, config map[string]any) (map[string]string, []*ast.Diagnostic) {
	t.Helper()
	if !bundled.Embedded {
		t.Skip("bundled files are not embedded")
	}

	fileNames := make([]string, 0, len(sources))
	for name := range sources {
		if strings.HasSuffix(name, ".ts") {
			fileNames = append(fileNames, name)
		}
	}

	fs := vfstest.FromMap(sources, true /*useCaseSensitiveFileNames*/)
	fs = bundled.WrapFS(fs)

	var host compiler.CompilerHost = compiler.NewCompilerHost("/src", fs, bundled.LibPath(), nil, nil, nil)
	host = &pluginCompilerHost{CompilerHost: host, plugins: []hooks.EmitPlugin{graphPlugin(t, config)}}

	options := baseOptions()
	options.Module = core.ModuleKindESNext

	program := compiler.NewProgram(compiler.ProgramOptions{
		Config: &tsoptions.ParsedCommandLine{
			ParsedConfig: &tsoptions.ParsedOptions{
				FileNames:       fileNames,
				CompilerOptions: options,
			},
		},
		Host: host,
	})

	ctx := context.Background()
	assert.Assert(t, len(program.GetSyntacticDiagnostics(ctx, nil)) == 0, "fixture has syntax errors")
	semantic := program.GetSemanticDiagnostics(ctx, nil)
	assert.Assert(t, len(semantic) == 0, "fixture has semantic errors: %v", semantic)

	var mu sync.Mutex
	outputs := map[string]string{}
	result := program.Emit(ctx, compiler.EmitOptions{
		WriteFile: func(fileName string, text string, data *compiler.WriteFileData) error {
			mu.Lock()
			outputs[fileName] = text
			mu.Unlock()
			return nil
		},
	})
	return outputs, result.Diagnostics
}

func TestGraphEmitsFactArtifact(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	outPath := filepath.Join(dir, "graph.json")

	sources := map[string]string{
		"/src/index.ts": `import { helper } from "./helpers";
export const value = helper();
`,
		"/src/helpers.ts": `export function helper() { return 1; }
export function orphan() { return 2; }
`,
	}
	outputs, diagnostics := emitGraphFixture(t, sources, map[string]any{"emit": outPath})
	assert.Assert(t, len(outputs) > 0)
	assertNoDiagnostics(t, diagnostics)

	data, err := os.ReadFile(outPath)
	assert.NilError(t, err, "graph JSON was not written to the real filesystem path")

	var g graph.Graph
	assert.NilError(t, json.Unmarshal(data, &g))
	assert.Assert(t, len(g.Files) == 2, "expected 2 files, got %d: %v", len(g.Files), g.Files)

	var sawEdge bool
	for _, edge := range g.Edges {
		if edge.Kind == graph.KindInternal && strings.HasSuffix(edge.From, "index.ts") && strings.HasSuffix(edge.To, "helpers.ts") {
			sawEdge = true
		}
	}
	assert.Assert(t, sawEdge, "expected an internal edge from index.ts to helpers.ts: %+v", g.Edges)

	var sawHelper, sawOrphan bool
	for _, export := range g.Exports {
		if export.Name == "helper" {
			sawHelper = true
		}
		if export.Name == "orphan" {
			sawOrphan = true
		}
	}
	assert.Assert(t, sawHelper && sawOrphan, "expected both exports recorded as facts: %+v", g.Exports)
}

func TestGraphInlineCycleDiagnostic(t *testing.T) {
	t.Parallel()
	sources := map[string]string{
		"/src/a.ts": `import "./b";
export const a = 1;
`,
		"/src/b.ts": `import "./a";
export const b = 1;
`,
	}
	_, diagnostics := emitGraphFixture(t, sources, map[string]any{
		"rules": []any{
			map[string]any{"module": "*", "cycle": "error"},
		},
	})
	assertDiagnosticContains(t, diagnostics, "import cycle")
}

func TestGraphInlinePhantomImportDiagnostic(t *testing.T) {
	t.Parallel()
	sources := map[string]string{
		"/src/index.ts": `import { x } from "left-pad";
export const value = x;
`,
		"/src/node_modules/left-pad/package.json": `{"name": "left-pad", "version": "1.0.0", "main": "./index.js", "types": "./index.d.ts"}`,
		"/src/node_modules/left-pad/index.js":     "module.exports = { x: 1 };\n",
		"/src/node_modules/left-pad/index.d.ts":   "export declare const x: number;\n",
	}
	_, diagnostics := emitGraphFixture(t, sources, map[string]any{
		"rules": []any{
			map[string]any{"module": "*", "phantomImport": "error"},
		},
	})
	assertDiagnosticContains(t, diagnostics, "left-pad")
}

// TestGraphInlineUnusedDiagnosticWithoutEmit verifies that "unused" IS
// evaluated by tsc-p itself when "emit" is not set: since this is a
// single, complete compilation with no separate graph-validate run,
// the plugin detects the graph is complete once every file's
// TakeDiagnostics has fired and evaluates unused for that final call.
func TestGraphInlineUnusedDiagnosticWithoutEmit(t *testing.T) {
	t.Parallel()
	sources := map[string]string{
		"/src/index.ts": `import { helper } from "./helpers";
export const value = helper();
`,
		"/src/helpers.ts": `export function helper() { return 1; }
`,
		"/src/orphan.ts": `export const neverImported = 1;
`,
	}
	_, diagnostics := emitGraphFixture(t, sources, map[string]any{
		"rules": []any{
			map[string]any{"module": "*", "unused": "error"},
		},
	})
	assertDiagnosticContains(t, diagnostics, "orphan.ts")
}

// TestGraphEmitAndUnusedRuleNotesItCannotBeEnforced verifies the
// unused-specific deferral note: unlike cycle/phantomImport/devLeak/
// importRules (deferred but still eventually checkable via
// graph-validate reading the same tsconfig), "unused" is never
// evaluated by tsc-p itself in ANY mode, so its own note fires whenever
// it is configured at all, in addition to the generic emit+rules note.
func TestGraphEmitAndUnusedRuleNotesItCannotBeEnforced(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	outPath := filepath.Join(dir, "graph.json")
	sources := map[string]string{
		"/src/index.ts": "export const value = 1;\n",
	}
	_, diagnostics := emitGraphFixture(t, sources, map[string]any{
		"emit": outPath,
		"rules": []any{
			map[string]any{"module": "*", "unused": "error"},
		},
	})
	assertDiagnosticContains(t, diagnostics, "\"unused\" is configured but is never evaluated by tsc-p itself")
}

func TestGraphEmitAndRulesTogetherNotesDeferral(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	outPath := filepath.Join(dir, "graph.json")
	sources := map[string]string{
		"/src/index.ts": "export const value = 1;\n",
	}
	_, diagnostics := emitGraphFixture(t, sources, map[string]any{
		"emit": outPath,
		"rules": []any{
			map[string]any{"module": "*", "cycle": "error"},
		},
	})
	assertDiagnosticContains(t, diagnostics, "graph-validate")
	_, err := os.Stat(outPath)
	assert.NilError(t, err, "graph JSON must still be written even though rules are deferred")
}
