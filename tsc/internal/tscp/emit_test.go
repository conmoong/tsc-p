// Integration tests for the tsc-p emit-plugin hook points, exercised
// through the full compiler emit pipeline. These tests use a minimal
// generic test plugin, not any concrete shipped plugin, to verify the
// hooks.EmitPlugin mechanism itself: placement, ordering, composition, and
// upstream-identical behaviour when no plugin is registered.

package tscp_test

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/microsoft/TypeScript/tsc/internal/ast"
	"github.com/microsoft/TypeScript/tsc/internal/bundled"
	"github.com/microsoft/TypeScript/tsc/internal/compiler"
	"github.com/microsoft/TypeScript/tsc/internal/core"
	"github.com/microsoft/TypeScript/tsc/internal/printer"
	"github.com/microsoft/TypeScript/tsc/internal/transformers"
	"github.com/microsoft/TypeScript/tsc/internal/tscp/hooks"
	"github.com/microsoft/TypeScript/tsc/internal/tsoptions"
	"github.com/microsoft/TypeScript/tsc/internal/vfs/vfstest"
	"gotest.tools/v3/assert"
)

// pluginCompilerHost wraps a compiler host with emit plugins, exercising
// the same optional-interface seam a real tsc-p host wrapper would use.
type pluginCompilerHost struct {
	compiler.CompilerHost
	plugins []hooks.EmitPlugin
}

func (h *pluginCompilerHost) TSCPEmitPlugins() []hooks.EmitPlugin {
	return h.plugins
}

// suffixTransformer appends a fixed suffix to every string literal in the
// file. It is a deliberately generic AST transform (it has no notion of
// import specifiers or any other specific syntax) used only to prove the
// hook mechanism works and is placed correctly in the pipeline.
type suffixTransformer struct {
	transformers.Transformer
	suffix string
}

func newSuffixTransformer(emitContext *printer.EmitContext, suffix string) *transformers.Transformer {
	tx := &suffixTransformer{suffix: suffix}
	return tx.NewTransformer(tx.visit, emitContext)
}

func (tx *suffixTransformer) visit(node *ast.Node) *ast.Node {
	if ast.IsStringLiteral(node) {
		return tx.Factory().NewStringLiteral(node.Text()+tx.suffix, node.AsStringLiteral().TokenFlags)
	}
	return tx.Visitor().VisitEachChild(node)
}

// suffixPlugin is a minimal hooks.EmitPlugin used only in these tests.
type suffixPlugin struct {
	suffix     string
	scriptHits atomic.Int32
	declHits   atomic.Int32
}

func (p *suffixPlugin) Name() string { return "test-suffix-plugin(" + p.suffix + ")" }

func (p *suffixPlugin) ScriptTransformer(emitContext *printer.EmitContext, host printer.EmitHost) *transformers.Transformer {
	p.scriptHits.Add(1)
	return newSuffixTransformer(emitContext, p.suffix)
}

func (p *suffixPlugin) DeclarationTransformer(emitContext *printer.EmitContext, host printer.EmitHost) *transformers.Transformer {
	p.declHits.Add(1)
	return newSuffixTransformer(emitContext, p.suffix)
}

var _ hooks.EmitPlugin = (*suffixPlugin)(nil)

var fixtureFiles = map[string]string{
	"/src/index.ts": `import { x } from "./dep";
import "./side";
export { y } from "./dep";
export const value = x + 1;
export async function load() {
    return import("./dyn");
}
`,
	"/src/dep.ts":  "export const x = 1;\nexport const y = 2;\n",
	"/src/side.ts": "export {};\n",
	"/src/dyn.ts":  "export const d = 3;\n",
}

func emitFixture(t *testing.T, files map[string]string, options *core.CompilerOptions, plugins []hooks.EmitPlugin) map[string]string {
	t.Helper()
	if !bundled.Embedded {
		t.Skip("bundled files are not embedded")
	}

	fs := vfstest.FromMap(files, true /*useCaseSensitiveFileNames*/)
	fs = bundled.WrapFS(fs)

	var host compiler.CompilerHost = compiler.NewCompilerHost("/src", fs, bundled.LibPath(), nil, nil, nil)
	if len(plugins) > 0 {
		host = &pluginCompilerHost{CompilerHost: host, plugins: plugins}
	}

	fileNames := make([]string, 0, len(files))
	for name := range files {
		if strings.HasSuffix(name, ".ts") {
			fileNames = append(fileNames, name)
		}
	}

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
	assert.Assert(t, len(program.GetSemanticDiagnostics(ctx, nil)) == 0, "fixture has semantic errors")

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
	assert.Assert(t, !result.EmitSkipped, "emit was skipped")
	assert.Assert(t, len(result.Diagnostics) == 0, "emit diagnostics: %v", result.Diagnostics)
	return outputs
}

func baseOptions() *core.CompilerOptions {
	return &core.CompilerOptions{
		Target:         core.ScriptTargetES2022,
		OutDir:         "/out",
		RootDir:        "/src",
		Declaration:    core.TSTrue,
		SourceMap:      core.TSTrue,
		DeclarationMap: core.TSTrue,
		Strict:         core.TSTrue,
	}
}

// TestNoPluginsIsByteIdenticalToUpstream verifies that a host providing no
// plugins (including all upstream hosts, which do not implement
// hooks.Provider at all) emits exactly as plain upstream does.
func TestNoPluginsIsByteIdenticalToUpstream(t *testing.T) {
	t.Parallel()

	for _, moduleKind := range []core.ModuleKind{core.ModuleKindESNext, core.ModuleKindCommonJS} {
		options := baseOptions()
		options.Module = moduleKind

		withoutHost := emitFixture(t, fixtureFiles, options, nil)
		withEmptyPluginList := emitFixture(t, fixtureFiles, options, []hooks.EmitPlugin{})

		assert.Assert(t, len(withoutHost) > 0, "no outputs emitted")
		assert.DeepEqual(t, withoutHost, withEmptyPluginList)
	}
}

// TestScriptHookRunsBeforeModuleLowering verifies that the script-pipeline
// hook point runs after import elision but before module lowering, for
// both ESM and CommonJS output: a plugin transform applied to every string
// literal is visible inside the lowered require() calls that the CommonJS
// transformer generates, proving one plugin run flows into both targets.
func TestScriptHookRunsBeforeModuleLowering(t *testing.T) {
	t.Parallel()

	files := map[string]string{
		"/src/index.ts": `import { x } from "./dep";
import "./side";
export { y } from "./dep";
export const value = x + 1;
export async function load() {
    return import("./dyn");
}
`,
		"/src/dep.ts":  "export const x = 1;\nexport const y = 2;\n",
		"/src/side.ts": "export {};\n",
		"/src/dyn.ts":  "export const d = 3;\n",
	}

	for _, moduleKind := range []core.ModuleKind{core.ModuleKindESNext, core.ModuleKindCommonJS} {
		options := baseOptions()
		options.Module = moduleKind

		plugin := &suffixPlugin{suffix: "-suf"}
		outputs := emitFixture(t, files, options, []hooks.EmitPlugin{plugin})
		js := outputs["/out/index.js"]

		switch moduleKind {
		case core.ModuleKindESNext:
			assert.Assert(t, strings.Contains(js, `"./dep-suf"`), "ESM import specifier not transformed:\n%s", js)
			assert.Assert(t, strings.Contains(js, `"./side-suf"`), "ESM side-effect import not transformed:\n%s", js)
			assert.Assert(t, strings.Contains(js, `"./dyn-suf"`), "ESM dynamic import not transformed:\n%s", js)
		case core.ModuleKindCommonJS:
			assert.Assert(t, strings.Contains(js, `require("./dep-suf")`), "lowered require does not carry the transform:\n%s", js)
			assert.Assert(t, strings.Contains(js, `require("./side-suf")`), "lowered side-effect require does not carry the transform:\n%s", js)
			assert.Assert(t, strings.Contains(js, `"./dyn-suf"`), "lowered dynamic import does not carry the transform:\n%s", js)
			assert.Assert(t, !strings.Contains(js, `"./dep"`), "untransformed specifier leaked into lowered output:\n%s", js)
		}
		assert.Assert(t, plugin.scriptHits.Load() >= int32(1), "script transformer was never constructed")
	}
}

// TestDeclarationHookRunsAfterDeclarationGeneration verifies that the
// declaration-pipeline hook point runs after the declaration transformer
// has constructed the declaration AST: the synthesised import type in
// load()'s return type only exists post-generation, so a transform
// reaching it proves the hook fires after that stage.
func TestDeclarationHookRunsAfterDeclarationGeneration(t *testing.T) {
	t.Parallel()

	options := baseOptions()
	options.Module = core.ModuleKindESNext

	plugin := &suffixPlugin{suffix: "-suf"}
	outputs := emitFixture(t, fixtureFiles, options, []hooks.EmitPlugin{plugin})

	dts := outputs["/out/index.d.ts"]
	assert.Assert(t, strings.Contains(dts, `"./dep-suf"`), "declaration export specifier not transformed:\n%s", dts)
	assert.Assert(t, strings.Contains(dts, `"./dyn-suf"`), "synthesised declaration import type not transformed:\n%s", dts)
	assert.Assert(t, plugin.declHits.Load() >= int32(1))
}

// TestSourceMapsAndDeclarationMapsSurviveTransforms verifies that source
// maps and declaration maps are still produced and reference the correct
// source files when a plugin is active.
func TestSourceMapsAndDeclarationMapsSurviveTransforms(t *testing.T) {
	t.Parallel()

	options := baseOptions()
	options.Module = core.ModuleKindESNext

	outputs := emitFixture(t, fixtureFiles, options, []hooks.EmitPlugin{&suffixPlugin{suffix: "-suf"}})

	assert.Assert(t, strings.Contains(outputs["/out/index.js"], "//# sourceMappingURL=index.js.map"))
	assert.Assert(t, strings.Contains(outputs["/out/index.js.map"], `"../src/index.ts"`), "js map sources wrong: %s", outputs["/out/index.js.map"])
	assert.Assert(t, strings.Contains(outputs["/out/index.d.ts.map"], `"../src/index.ts"`), "dts map sources wrong: %s", outputs["/out/index.d.ts.map"])
}

// TestCommentsSurviveTransforms verifies that comments around a
// transformed node are preserved.
func TestCommentsSurviveTransforms(t *testing.T) {
	t.Parallel()

	files := map[string]string{
		"/src/index.ts": "// keep this comment\nimport { x } from \"./dep\";\nexport const value = x;\n",
		"/src/dep.ts":   "export const x = 1;\n",
	}
	options := baseOptions()
	options.Module = core.ModuleKindESNext

	outputs := emitFixture(t, files, options, []hooks.EmitPlugin{&suffixPlugin{suffix: "-suf"}})
	js := outputs["/out/index.js"]
	assert.Assert(t, strings.Contains(js, "// keep this comment"), "comment lost:\n%s", js)
	assert.Assert(t, strings.Contains(js, `"./dep-suf"`), "transform did not apply:\n%s", js)
}

// TestMultiplePluginsComposeInOrder verifies that plugins registered by a
// host run in order, each seeing the previous plugin's output.
func TestMultiplePluginsComposeInOrder(t *testing.T) {
	t.Parallel()

	files := map[string]string{
		"/src/index.ts": `export const value = "marker";`,
	}
	options := baseOptions()
	options.Module = core.ModuleKindESNext

	first := &suffixPlugin{suffix: "-a"}
	second := &suffixPlugin{suffix: "-b"}
	outputs := emitFixture(t, files, options, []hooks.EmitPlugin{first, second})

	js := outputs["/out/index.js"]
	assert.Assert(t, strings.Contains(js, `"marker-a-b"`), "plugins did not compose in registration order:\n%s", js)
}
