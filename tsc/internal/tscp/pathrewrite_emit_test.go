// Integration tests for the pathrewrite plugin through the full compiler
// emit pipeline: tsconfig path aliases become relative paths, extensions
// are derived from the resolved file (.ts -> .js, .mts -> .mjs,
// .cts -> .cjs, .json kept), package imports stay untouched, and with the
// plugin inactive output is byte-identical to upstream.

package tscp_test

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/microsoft/TypeScript/tsc/internal/bundled"
	"github.com/microsoft/TypeScript/tsc/internal/collections"
	"github.com/microsoft/TypeScript/tsc/internal/compiler"
	"github.com/microsoft/TypeScript/tsc/internal/core"
	"github.com/microsoft/TypeScript/tsc/internal/tscp/hooks"
	"github.com/microsoft/TypeScript/tsc/internal/tscp/pathrewrite"
	"github.com/microsoft/TypeScript/tsc/internal/tsoptions"
	"github.com/microsoft/TypeScript/tsc/internal/vfs/vfstest"
	"gotest.tools/v3/assert"
)

// aliasFixtureFiles is a project exercising every rewrite rule: a wildcard
// alias to .ts files, exact aliases to .mts/.cts/.json files, an
// extensionless relative import, and a node_modules package.
var aliasFixtureFiles = map[string]string{
	"/src/index.ts": `import { alpha } from "@lib/alpha";
import type { AlphaType } from "@lib/alpha";
import gammaValue from "@g";
import deltaValue from "@c";
import config from "@data";
import { beta } from "./beta";
import pkg from "somepkg";
export { alpha } from "@lib/alpha";
export const total: AlphaType = alpha + gammaValue + deltaValue + beta + pkg + config.count;
export function loadGamma() {
    return import("@g");
}
`,
	"/src/lib/alpha.ts":  "export const alpha = 1;\nexport type AlphaType = number;\n",
	"/src/lib/gamma.mts": "export default 2;\n",
	"/src/lib/delta.cts": "declare const value: number;\nexport = value;\n",
	"/src/beta.ts":       "export const beta = 4;\n",
	"/src/data/config.json": `{ "count": 5 }
`,
	"/src/node_modules/somepkg/package.json": `{ "name": "somepkg", "version": "1.0.0", "types": "index.d.ts", "main": "index.js" }`,
	"/src/node_modules/somepkg/index.d.ts":   "declare const x: number;\nexport default x;\n",
	"/src/node_modules/somepkg/index.js":     "module.exports = 8;\n",
}

func aliasOptions(moduleKind core.ModuleKind) *core.CompilerOptions {
	paths := &collections.OrderedMap[string, []string]{}
	paths.Set("@lib/*", []string{"./lib/*"})
	paths.Set("@g", []string{"./lib/gamma.mts"})
	paths.Set("@c", []string{"./lib/delta.cts"})
	paths.Set("@data", []string{"./data/config.json"})
	return &core.CompilerOptions{
		Module:            moduleKind,
		Target:            core.ScriptTargetES2022,
		OutDir:            "/out",
		RootDir:           "/src",
		Declaration:       core.TSTrue,
		SourceMap:         core.TSTrue,
		Strict:            core.TSTrue,
		ESModuleInterop:   core.TSTrue,
		ResolveJsonModule: core.TSTrue,
		Paths:             paths,
		PathsBasePath:     "/src",
	}
}

// emitAliasProject compiles the alias fixture with the given plugins (nil =
// a plain upstream host) and returns the emitted outputs by file name.
func emitAliasProject(t *testing.T, files map[string]string, options *core.CompilerOptions, plugins []hooks.EmitPlugin) map[string]string {
	t.Helper()
	return emitAliasProjectWithRaw(t, files, options, plugins, nil)
}

// rawConfigWithPluginEntry builds a raw parsed-tsconfig shape carrying one
// compilerOptions.plugins entry for this plugin, as the production
// activation path reads it.
func rawConfigWithPluginEntry(entry map[string]any) any {
	entry["name"] = pathrewrite.PluginName
	return map[string]any{
		"compilerOptions": map[string]any{
			"plugins": []any{entry},
		},
	}
}

func emitAliasProjectWithRaw(t *testing.T, files map[string]string, options *core.CompilerOptions, plugins []hooks.EmitPlugin, raw any) map[string]string {
	t.Helper()
	if !bundled.Embedded {
		t.Skip("bundled files are not embedded")
	}

	fs := vfstest.FromMap(files, true /*useCaseSensitiveFileNames*/)
	fs = bundled.WrapFS(fs)

	var host compiler.CompilerHost = compiler.NewCompilerHost("/src", fs, bundled.LibPath(), nil, nil, nil)
	if plugins != nil {
		host = &pluginCompilerHost{CompilerHost: host, plugins: plugins}
	}

	program := compiler.NewProgram(compiler.ProgramOptions{
		Config: &tsoptions.ParsedCommandLine{
			ParsedConfig: &tsoptions.ParsedOptions{
				FileNames:       []string{"/src/index.ts"},
				CompilerOptions: options,
			},
			Raw: raw,
		},
		Host: host,
	})

	ctx := context.Background()
	assert.Assert(t, len(program.GetSyntacticDiagnostics(ctx, nil)) == 0, "fixture has syntax errors")
	assert.Assert(t, len(program.GetSemanticDiagnostics(ctx, nil)) == 0, "fixture has semantic errors: %v", program.GetSemanticDiagnostics(ctx, nil))

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

var hostPlugins = []hooks.EmitPlugin{pathrewrite.NewHostPlugin(nil)}

func TestPathRewriteESMAliases(t *testing.T) {
	t.Parallel()

	outputs := emitAliasProject(t, aliasFixtureFiles, aliasOptions(core.ModuleKindESNext), hostPlugins)
	js := outputs["/out/index.js"]

	assert.Assert(t, strings.Contains(js, `from "./lib/alpha.js"`), "wildcard alias to .ts not rewritten:\n%s", js)
	assert.Assert(t, strings.Contains(js, `from "./lib/gamma.mjs"`), ".mts destination not rewritten to .mjs:\n%s", js)
	assert.Assert(t, strings.Contains(js, `from "./lib/delta.cjs"`), ".cts destination not rewritten to .cjs:\n%s", js)
	assert.Assert(t, strings.Contains(js, `from "./data/config.json"`), ".json destination must keep .json:\n%s", js)
	assert.Assert(t, strings.Contains(js, `from "./beta.js"`), "extensionless relative import not given an extension:\n%s", js)
	assert.Assert(t, strings.Contains(js, `from "somepkg"`), "package import must stay untouched:\n%s", js)
	assert.Assert(t, strings.Contains(js, `import("./lib/gamma.mjs")`), "dynamic import alias not rewritten:\n%s", js)
	assert.Assert(t, !strings.Contains(js, `"@lib/`) && !strings.Contains(js, `"@g"`) && !strings.Contains(js, `"@c"`) && !strings.Contains(js, `"@data"`),
		"an alias leaked into the output:\n%s", js)
}

func TestPathRewriteCommonJSAliases(t *testing.T) {
	t.Parallel()

	files := map[string]string{}
	for name, text := range aliasFixtureFiles {
		files[name] = text
	}
	files["/src/index.ts"] = `import { alpha } from "@lib/alpha";
import delta = require("@c");
import { beta } from "./beta";
export const total: number = alpha + delta + beta;
export function loadGamma() {
    return import("@g");
}
`

	outputs := emitAliasProject(t, files, aliasOptions(core.ModuleKindCommonJS), hostPlugins)
	js := outputs["/out/index.js"]

	assert.Assert(t, strings.Contains(js, `require("./lib/alpha.js")`), "lowered require does not carry the rewritten alias:\n%s", js)
	assert.Assert(t, strings.Contains(js, `require("./lib/delta.cjs")`), "import-equals alias not rewritten:\n%s", js)
	assert.Assert(t, strings.Contains(js, `require("./beta.js")`), "extensionless relative not rewritten in CJS:\n%s", js)
	assert.Assert(t, strings.Contains(js, `"./lib/gamma.mjs"`), "lowered dynamic import alias not rewritten:\n%s", js)
	assert.Assert(t, !strings.Contains(js, `"@lib/`) && !strings.Contains(js, `"@c"`) && !strings.Contains(js, `"@g"`),
		"an alias leaked into the CJS output:\n%s", js)
}

func TestPathRewriteDeclarationAliases(t *testing.T) {
	t.Parallel()

	outputs := emitAliasProject(t, aliasFixtureFiles, aliasOptions(core.ModuleKindESNext), hostPlugins)
	dts := outputs["/out/index.d.ts"]

	assert.Assert(t, strings.Contains(dts, `from "./lib/alpha.js"`), "declaration export alias not rewritten:\n%s", dts)
	// loadGamma's return type is a synthesised import type that only exists
	// after declaration generation.
	assert.Assert(t, strings.Contains(dts, `import("./lib/gamma.mjs")`), "synthesised declaration import type not rewritten:\n%s", dts)
	assert.Assert(t, !strings.Contains(dts, `"@lib/`) && !strings.Contains(dts, `"@g"`), "an alias leaked into the declaration output:\n%s", dts)
}

// TestPathRewriteJsxRuntimeImport verifies that the automatic JSX runtime
// import synthesised from a relative jsxImportSource is rewritten. This
// specifier is never walked by the file loader (there is no explicit
// import statement for it anywhere in the source) — the checker resolves
// it for itself via its own resolveExternalModule call, so it is absent
// from the program's resolvedModules cache the way a real import would be
// present; only a live re-resolution (the plugin's third, last-resort
// tier) can find it.
func TestPathRewriteJsxRuntimeImport(t *testing.T) {
	t.Parallel()

	files := map[string]string{
		"/src/index.tsx": `const el = <div>hi</div>;
export const rendered = String(el);
`,
		"/src/shim/jsx-runtime/index.ts": `export function jsx(type: unknown, props: unknown): unknown {
    return { type, props };
}
export const jsxs = jsx;
declare global {
    namespace JSX {
        interface IntrinsicElements {
            [name: string]: Record<string, unknown>;
        }
    }
}
`,
	}
	options := &core.CompilerOptions{
		Module:           core.ModuleKindESNext,
		ModuleResolution: core.ModuleResolutionKindBundler,
		Target:           core.ScriptTargetES2022,
		OutDir:           "/out",
		RootDir:          "/src",
		Jsx:              core.JsxEmitReactJSX,
		JsxImportSource:  "./shim",
		Strict:           core.TSTrue,
	}

	fs := vfstest.FromMap(files, true /*useCaseSensitiveFileNames*/)
	fs = bundled.WrapFS(fs)
	var host compiler.CompilerHost = compiler.NewCompilerHost("/src", fs, bundled.LibPath(), nil, nil, nil)
	host = &pluginCompilerHost{CompilerHost: host, plugins: hostPlugins}

	program := compiler.NewProgram(compiler.ProgramOptions{
		Config: &tsoptions.ParsedCommandLine{
			ParsedConfig: &tsoptions.ParsedOptions{
				FileNames:       []string{"/src/index.tsx"},
				CompilerOptions: options,
			},
		},
		Host: host,
	})

	ctx := context.Background()
	assert.Assert(t, len(program.GetSyntacticDiagnostics(ctx, nil)) == 0, "fixture has syntax errors")
	assert.Assert(t, len(program.GetSemanticDiagnostics(ctx, nil)) == 0, "fixture has semantic errors: %v", program.GetSemanticDiagnostics(ctx, nil))

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

	js := outputs["/out/index.js"]
	assert.Assert(t, strings.Contains(js, `from "./shim/jsx-runtime/index.js"`), "synthesised JSX runtime import not rewritten:\n%s", js)
	assert.Assert(t, !strings.Contains(js, `from "./shim/jsx-runtime";`) && !strings.Contains(js, `from "./shim/jsx-runtime"\n`),
		"unresolved/unextended JSX runtime specifier leaked into output:\n%s", js)
}

// TestPathRewriteInactiveMatchesUpstream verifies byte-parity with plain
// upstream emit when the plugin is not active: aliases are left exactly as
// written.
func TestPathRewriteInactiveMatchesUpstream(t *testing.T) {
	t.Parallel()

	baseline := emitAliasProject(t, aliasFixtureFiles, aliasOptions(core.ModuleKindESNext), nil)
	js := baseline["/out/index.js"]
	assert.Assert(t, strings.Contains(js, `from "@lib/alpha"`), "without the plugin, aliases must stay as written:\n%s", js)

	// An empty plugin list behaves identically.
	withEmpty := emitAliasProject(t, aliasFixtureFiles, aliasOptions(core.ModuleKindESNext), []hooks.EmitPlugin{})
	assert.DeepEqual(t, baseline, withEmpty)
}

// TestPathRewriteAlreadyCorrectSpecifiersUnchanged verifies that a project
// whose imports are already relative with correct extensions emits
// byte-identically whether or not the plugin is active.
func TestPathRewriteAlreadyCorrectSpecifiersUnchanged(t *testing.T) {
	t.Parallel()

	files := map[string]string{
		"/src/index.ts": `import { beta } from "./beta.js";
export const value = beta;
`,
		"/src/beta.ts": "export const beta = 4;\n",
	}
	options := aliasOptions(core.ModuleKindESNext)

	withPlugin := emitAliasProject(t, files, options, hostPlugins)
	without := emitAliasProject(t, files, options, nil)
	assert.DeepEqual(t, withPlugin, without)
}

// TestPathRewriteConfigActivation verifies the production wiring: with a
// plugins entry in the parsed tsconfig and a plain upstream host (no plugin
// provider), the emit host activates the plugin from the configuration.
func TestPathRewriteConfigActivation(t *testing.T) {
	t.Parallel()

	raw := rawConfigWithPluginEntry(map[string]any{})
	outputs := emitAliasProjectWithRaw(t, aliasFixtureFiles, aliasOptions(core.ModuleKindESNext), nil, raw)
	js := outputs["/out/index.js"]
	assert.Assert(t, strings.Contains(js, `from "./lib/alpha.js"`), "config activation did not rewrite:\n%s", js)
	assert.Assert(t, strings.Contains(js, `from "somepkg"`), "package import must stay untouched:\n%s", js)
}

// TestPathRewritePerAliasRules verifies fine-grained per-alias control:
// disabling one alias, and an extensionless rewrite for another, while
// unmatched aliases stay enabled with the global extension setting.
func TestPathRewritePerAliasRules(t *testing.T) {
	t.Parallel()

	raw := rawConfigWithPluginEntry(map[string]any{
		"alias": map[string]any{
			"@g": false,
			"@c": map[string]any{"enabled": true, "extension": false},
		},
	})
	outputs := emitAliasProjectWithRaw(t, aliasFixtureFiles, aliasOptions(core.ModuleKindESNext), nil, raw)
	js := outputs["/out/index.js"]

	assert.Assert(t, strings.Contains(js, `from "@g"`), "disabled alias must stay as written:\n%s", js)
	assert.Assert(t, strings.Contains(js, `import("@g")`), "disabled alias must stay as written in dynamic imports:\n%s", js)
	assert.Assert(t, strings.Contains(js, `from "./lib/delta"`), "extensionless alias rewrite missing:\n%s", js)
	assert.Assert(t, !strings.Contains(js, `"./lib/delta.cjs"`), "extension applied despite per-alias extension=false:\n%s", js)
	assert.Assert(t, strings.Contains(js, `from "./lib/alpha.js"`), "unmatched alias must stay enabled with extensions:\n%s", js)
}

// TestPathRewriteGlobalExtensionOff verifies extension=false: rewrites are
// extensionless, except .json which always keeps its extension, and
// already-extensionless relative imports come out unchanged.
func TestPathRewriteGlobalExtensionOff(t *testing.T) {
	t.Parallel()

	raw := rawConfigWithPluginEntry(map[string]any{"extension": false})
	outputs := emitAliasProjectWithRaw(t, aliasFixtureFiles, aliasOptions(core.ModuleKindESNext), nil, raw)
	js := outputs["/out/index.js"]

	assert.Assert(t, strings.Contains(js, `from "./lib/alpha"`), "alias should rewrite extensionless:\n%s", js)
	assert.Assert(t, !strings.Contains(js, `"./lib/alpha.js"`), "extension applied despite extension=false:\n%s", js)
	assert.Assert(t, strings.Contains(js, `from "./data/config.json"`), ".json must keep its extension even with extension=false:\n%s", js)
	assert.Assert(t, strings.Contains(js, `from "./beta"`), "relative import should stay extensionless:\n%s", js)
}

// TestPathRewriteRelativeOff verifies that relative imports are addressed
// through the same alias map as everything else: "./*" (and "../*") rules
// leave relative imports untouched while aliases are still rewritten.
func TestPathRewriteRelativeOff(t *testing.T) {
	t.Parallel()

	raw := rawConfigWithPluginEntry(map[string]any{
		"alias": map[string]any{"./*": false, "../*": false},
	})
	outputs := emitAliasProjectWithRaw(t, aliasFixtureFiles, aliasOptions(core.ModuleKindESNext), nil, raw)
	js := outputs["/out/index.js"]

	assert.Assert(t, strings.Contains(js, `from "./beta"`), "relative import must stay as written:\n%s", js)
	assert.Assert(t, !strings.Contains(js, `"./beta.js"`), "relative import rewritten despite \"./*\": false:\n%s", js)
	assert.Assert(t, strings.Contains(js, `from "./lib/alpha.js"`), "aliases must still be rewritten:\n%s", js)
}

// TestPathRewriteDeclarationsOff verifies declarations=false: JavaScript is
// rewritten while declaration output keeps the aliases as written.
func TestPathRewriteDeclarationsOff(t *testing.T) {
	t.Parallel()

	raw := rawConfigWithPluginEntry(map[string]any{"declarations": false})
	outputs := emitAliasProjectWithRaw(t, aliasFixtureFiles, aliasOptions(core.ModuleKindESNext), nil, raw)

	js := outputs["/out/index.js"]
	assert.Assert(t, strings.Contains(js, `from "./lib/alpha.js"`), "script output must still be rewritten:\n%s", js)

	dts := outputs["/out/index.d.ts"]
	assert.Assert(t, strings.Contains(dts, `from "@lib/alpha"`), "declaration output must stay as written with declarations=false:\n%s", dts)
}

// TestPathRewritePackageJsonImports verifies that aliases defined through
// package.json "imports" (subpath imports, "#...") are rewritten the same
// way as tsconfig paths aliases: the rewriter works from the compiler's
// resolution results, whatever mechanism produced them.
func TestPathRewritePackageJsonImports(t *testing.T) {
	t.Parallel()

	files := map[string]string{
		"/src/package.json": `{ "name": "app", "type": "module", "imports": { "#lib/*": "./lib/*.ts" } }`,
		"/src/index.ts": `import { alpha } from "#lib/alpha";
export const value = alpha + 1;
`,
		"/src/lib/alpha.ts": "export const alpha = 1;\n",
	}
	options := &core.CompilerOptions{
		Module:      core.ModuleKindESNext,
		Target:      core.ScriptTargetES2022,
		OutDir:      "/out",
		RootDir:     "/src",
		Declaration: core.TSTrue,
		Strict:      core.TSTrue,
	}

	outputs := emitAliasProjectWithRaw(t, files, options, nil, rawConfigWithPluginEntry(map[string]any{}))
	js := outputs["/out/index.js"]
	assert.Assert(t, strings.Contains(js, `from "./lib/alpha.js"`), "package.json imports alias not rewritten:\n%s", js)
	assert.Assert(t, !strings.Contains(js, `"#lib/`), "package.json imports alias leaked into output:\n%s", js)
}
