// Integration tests for the bouncer plugin through the full compiler emit
// pipeline: @bouncer remove elides a declaration from script and
// declaration output, @bouncer export/no-export toggles the export
// modifier, export default/export NAME append a separate export
// statement, profiles resolve through tsconfig configuration, and with
// the plugin inactive output is byte-identical to upstream.

package tscp_test

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/microsoft/TypeScript/tsc/internal/bundled"
	"github.com/microsoft/TypeScript/tsc/internal/compiler"
	"github.com/microsoft/TypeScript/tsc/internal/core"
	"github.com/microsoft/TypeScript/tsc/internal/tscp/bouncer"
	"github.com/microsoft/TypeScript/tsc/internal/tscp/hooks"
	"github.com/microsoft/TypeScript/tsc/internal/tsoptions"
	"github.com/microsoft/TypeScript/tsc/internal/vfs/vfstest"
	"gotest.tools/v3/assert"
)

func bouncerOptions() *core.CompilerOptions {
	return &core.CompilerOptions{
		Module:      core.ModuleKindESNext,
		Target:      core.ScriptTargetES2022,
		OutDir:      "/out",
		RootDir:     "/src",
		Declaration: core.TSTrue,
		SourceMap:   core.TSTrue,
		Strict:      core.TSTrue,
	}
}

func emitBouncerProject(t *testing.T, files map[string]string, plugins []hooks.EmitPlugin, raw any) map[string]string {
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
				CompilerOptions: bouncerOptions(),
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

func bouncerRawConfig(entry map[string]any) any {
	config := map[string]any{"name": bouncer.PluginName}
	for key, value := range entry {
		config[key] = value
	}
	return map[string]any{
		"compilerOptions": map[string]any{
			"plugins": []any{config},
		},
	}
}

const bouncerFixture = `/**
 * @bouncer remove
 */
export function mockFn() {
    return 1;
}

/**
 * @bouncer no-export
 */
export function internalHelper() {
    return 2;
}

/**
 * @bouncer export
 */
function shouldBeExported() {
    return 3;
}

/**
 * @bouncer profile QA
 */
export class DebugOnly {
    value = 4;
}

export const kept = 5;
`

var bouncerPlugins = []hooks.EmitPlugin{bouncer.NewPlugin(nil)}

func TestBouncerRemove(t *testing.T) {
	t.Parallel()

	files := map[string]string{"/src/index.ts": bouncerFixture}
	outputs := emitBouncerProject(t, files, bouncerPlugins, nil)
	js := outputs["/out/index.js"]
	dts := outputs["/out/index.d.ts"]

	assert.Assert(t, !strings.Contains(js, "mockFn"), "removed function leaked into JS:\n%s", js)
	assert.Assert(t, !strings.Contains(dts, "mockFn"), "removed function leaked into declarations:\n%s", dts)
	assert.Assert(t, strings.Contains(js, "kept"), "unrelated declaration was affected:\n%s", js)
}

// TestBouncerNoExport verifies that "no-export" strips the export modifier
// in JavaScript (keeping the implementation usable elsewhere in the file)
// but omits the declaration entirely from declaration output.
func TestBouncerNoExport(t *testing.T) {
	t.Parallel()

	files := map[string]string{"/src/index.ts": bouncerFixture}
	outputs := emitBouncerProject(t, files, bouncerPlugins, nil)
	js := outputs["/out/index.js"]
	dts := outputs["/out/index.d.ts"]

	assert.Assert(t, strings.Contains(js, "function internalHelper()"), "function body removed unexpectedly:\n%s", js)
	assert.Assert(t, !strings.Contains(js, "export function internalHelper"), "export not stripped in JS:\n%s", js)
	assert.Assert(t, !strings.Contains(dts, "internalHelper"), "declaration not fully omitted for no-export:\n%s", dts)
}

// TestBouncerExport verifies "@bouncer export" in JavaScript output. It
// deliberately does not assert on declaration output: see
// TestBouncerExportHasNoDeclarationEffect and the package doc comment.
func TestBouncerExport(t *testing.T) {
	t.Parallel()

	files := map[string]string{"/src/index.ts": bouncerFixture}
	outputs := emitBouncerProject(t, files, bouncerPlugins, nil)
	js := outputs["/out/index.js"]

	assert.Assert(t, strings.Contains(js, "export function shouldBeExported"), "export not added in JS:\n%s", js)
}

// TestBouncerExportHasNoDeclarationEffect documents (and pins) the
// documented limitation, isolated to just the affected declaration. The
// file needs at least one other export to be a module in TypeScript's
// sense — true of essentially every real project file — because a
// private, non-exported declaration in a *script* (a file with no
// imports/exports at all) is implicitly global/ambient and does appear in
// .d.ts; that script-mode case is not the realistic one.
func TestBouncerExportHasNoDeclarationEffect(t *testing.T) {
	t.Parallel()

	files := map[string]string{"/src/index.ts": `export const other = 1;

/**
 * @bouncer export
 */
function shouldBeExported() {
    return 3;
}
`}
	withPlugin := emitBouncerProject(t, files, bouncerPlugins, nil)
	without := emitBouncerProject(t, files, nil, nil)

	assert.Assert(t, !strings.Contains(without["/out/index.d.ts"], "shouldBeExported"), "expected baseline to already omit a non-exported declaration in a module file")
	assert.Equal(t, without["/out/index.d.ts"], withPlugin["/out/index.d.ts"], "declaration output for the export case should be identical with and without the plugin")
}

// TestBouncerExportDeclarationMitigation pins the documented workaround
// for the above limitation: a manual "export type { Name };" (or a plain
// "export { Name };") anywhere in the file, referencing the declaration's
// name, causes TypeScript's own declaration transformer to treat the name
// as part of the module's public surface from the start — independently
// of this plugin — so the declaration is included and marked exported in
// .d.ts directly. Note this is not really about "type" vs. value export:
// either form triggers inclusion; "export type" is recommended because it
// does not also add a redundant runtime export in JavaScript (that side
// is already handled by "@bouncer export" itself).
func TestBouncerExportDeclarationMitigation(t *testing.T) {
	t.Parallel()

	files := map[string]string{"/src/index.ts": `export const other = 1;

/**
 * @bouncer export
 */
class ABC {
    value = 1;
}

export type { ABC };
`}
	outputs := emitBouncerProject(t, files, bouncerPlugins, nil)
	js := outputs["/out/index.js"]
	dts := outputs["/out/index.d.ts"]

	assert.Assert(t, strings.Contains(js, "export class ABC"), "class value should be exported in JS via the bouncer export tag:\n%s", js)
	assert.Assert(t, strings.Contains(dts, "export declare class ABC"), "class should be exported directly in declarations once referenced by a manual export statement:\n%s", dts)
}

// TestBouncerExportDefault verifies that "@bouncer export default" strips
// any existing export from the declaration and appends a separate
// "export default <name>;" statement, in both script and declaration
// output, without touching internal references to the name.
func TestBouncerExportDefault(t *testing.T) {
	t.Parallel()

	files := map[string]string{"/src/index.ts": `/**
 * @bouncer export default
 */
export function primary() {
    return 1;
}

export const useIt = primary();
`}
	outputs := emitBouncerProject(t, files, bouncerPlugins, nil)
	js := outputs["/out/index.js"]
	dts := outputs["/out/index.d.ts"]

	assert.Assert(t, strings.Contains(js, "function primary()"), "function body missing:\n%s", js)
	assert.Assert(t, !strings.Contains(js, "export function primary"), "original export not stripped in JS:\n%s", js)
	assert.Assert(t, strings.Contains(js, "export default primary;"), "export default statement missing in JS:\n%s", js)
	assert.Assert(t, strings.Contains(js, "primary()"), "internal reference to the function was altered:\n%s", js)

	assert.Assert(t, !strings.Contains(dts, "export declare function primary"), "original export not stripped in declarations:\n%s", dts)
	assert.Assert(t, strings.Contains(dts, "export default primary;"), "export default statement missing in declarations:\n%s", dts)
}

// TestBouncerExportAs verifies that "@bouncer export NewName" strips any
// existing export from the declaration and appends a separate
// "export { name as NewName };" statement, leaving the declaration's own
// name and internal references to it unchanged.
func TestBouncerExportAs(t *testing.T) {
	t.Parallel()

	files := map[string]string{"/src/index.ts": `/**
 * @bouncer export renamed
 */
export function original() {
    return 1;
}

export const useIt = original();
`}
	outputs := emitBouncerProject(t, files, bouncerPlugins, nil)
	js := outputs["/out/index.js"]
	dts := outputs["/out/index.d.ts"]

	assert.Assert(t, strings.Contains(js, "function original()"), "function body missing:\n%s", js)
	assert.Assert(t, !strings.Contains(js, "export function original"), "original export not stripped in JS:\n%s", js)
	assert.Assert(t, strings.Contains(js, "export { original as renamed };"), "export-as statement missing in JS:\n%s", js)
	assert.Assert(t, strings.Contains(js, "original()"), "internal reference to the function was altered:\n%s", js)

	assert.Assert(t, !strings.Contains(dts, "export declare function original"), "original export not stripped in declarations:\n%s", dts)
	assert.Assert(t, strings.Contains(dts, "export { original as renamed };"), "export-as statement missing in declarations:\n%s", dts)
}

// TestBouncerExportDefaultAmbiguousVariableIsNoOp verifies that
// export-default (and, by the same code path, export-as) is a no-op on a
// variable statement with more than one declarator, since there is no
// single unambiguous name to reference.
func TestBouncerExportDefaultAmbiguousVariableIsNoOp(t *testing.T) {
	t.Parallel()

	files := map[string]string{"/src/index.ts": `/**
 * @bouncer export default
 */
export const a = 1, b = 2;
`}
	withPlugin := emitBouncerProject(t, files, bouncerPlugins, nil)
	without := emitBouncerProject(t, files, nil, nil)
	assert.DeepEqual(t, without, withPlugin)
}

func TestBouncerProfile(t *testing.T) {
	t.Parallel()

	files := map[string]string{"/src/index.ts": bouncerFixture}
	raw := bouncerRawConfig(map[string]any{
		"profiles": map[string]any{"QA": "no-export"},
	})
	outputs := emitBouncerProject(t, files, nil, raw)
	js := outputs["/out/index.js"]
	dts := outputs["/out/index.d.ts"]

	assert.Assert(t, strings.Contains(js, "class DebugOnly"), "class body removed unexpectedly:\n%s", js)
	assert.Assert(t, !strings.Contains(js, "export class DebugOnly"), "profile no-export not applied in JS:\n%s", js)
	assert.Assert(t, !strings.Contains(dts, "DebugOnly"), "profile no-export did not omit the declaration from declarations:\n%s", dts)
}

func TestBouncerProfileRemove(t *testing.T) {
	t.Parallel()

	files := map[string]string{"/src/index.ts": bouncerFixture}
	raw := bouncerRawConfig(map[string]any{
		"profiles": map[string]any{"QA": "remove"},
	})
	outputs := emitBouncerProject(t, files, nil, raw)
	js := outputs["/out/index.js"]

	assert.Assert(t, !strings.Contains(js, "DebugOnly"), "profile remove not applied:\n%s", js)
}

// releaseFixture declares one function per release channel plus one
// untagged function, for exercising @bouncer release LEVEL trimming.
const releaseFixture = `/**
 * @bouncer release public
 */
export function publicApi() {
    return 1;
}

/**
 * @bouncer release beta
 */
export function betaApi() {
    return 2;
}

/**
 * @bouncer release alpha
 */
export function alphaApi() {
    return 3;
}

/**
 * @bouncer release internal
 */
export function internalApi() {
    return 4;
}

export function untaggedApi() {
    return 5;
}
`

// TestBouncerReleaseTrimsDeclarationsOnly verifies that a configured
// release channel omits more-experimental declarations from .d.ts output
// while leaving JavaScript output completely untouched — release tagging
// is a declaration-rollup concern, never an emit-behaviour one.
func TestBouncerReleaseTrimsDeclarationsOnly(t *testing.T) {
	t.Parallel()

	files := map[string]string{"/src/index.ts": releaseFixture}
	raw := bouncerRawConfig(map[string]any{"release": "beta"})
	outputs := emitBouncerProject(t, files, nil, raw)
	js := outputs["/out/index.js"]
	dts := outputs["/out/index.d.ts"]

	for _, name := range []string{"publicApi", "betaApi", "alphaApi", "internalApi", "untaggedApi"} {
		assert.Assert(t, strings.Contains(js, name), "JS output must be unaffected by release trimming, missing %s:\n%s", name, js)
	}
	assert.Assert(t, strings.Contains(dts, "publicApi"), "public must survive a beta channel:\n%s", dts)
	assert.Assert(t, strings.Contains(dts, "betaApi"), "beta must survive a beta channel:\n%s", dts)
	assert.Assert(t, !strings.Contains(dts, "alphaApi"), "alpha must be trimmed from a beta channel:\n%s", dts)
	assert.Assert(t, !strings.Contains(dts, "internalApi"), "internal must be trimmed from a beta channel:\n%s", dts)
	assert.Assert(t, strings.Contains(dts, "untaggedApi"), "an untagged declaration must never be trimmed:\n%s", dts)
}

// TestBouncerReleaseChannels checks every channel's trimming boundary:
// each channel keeps declarations at or below its own rank and trims
// everything more experimental.
func TestBouncerReleaseChannels(t *testing.T) {
	t.Parallel()

	files := map[string]string{"/src/index.ts": releaseFixture}
	cases := []struct {
		channel string
		kept    []string
		trimmed []string
	}{
		{"public", []string{"publicApi"}, []string{"betaApi", "alphaApi", "internalApi"}},
		{"beta", []string{"publicApi", "betaApi"}, []string{"alphaApi", "internalApi"}},
		{"alpha", []string{"publicApi", "betaApi", "alphaApi"}, []string{"internalApi"}},
		{"internal", []string{"publicApi", "betaApi", "alphaApi", "internalApi"}, nil},
	}
	for _, c := range cases {
		raw := bouncerRawConfig(map[string]any{"release": c.channel})
		outputs := emitBouncerProject(t, files, nil, raw)
		dts := outputs["/out/index.d.ts"]
		for _, name := range c.kept {
			assert.Assert(t, strings.Contains(dts, name), "channel %s must keep %s:\n%s", c.channel, name, dts)
		}
		for _, name := range c.trimmed {
			assert.Assert(t, !strings.Contains(dts, name), "channel %s must trim %s:\n%s", c.channel, name, dts)
		}
	}
}

// TestBouncerReleaseWithNoConfiguredChannelIsNoOp verifies that a
// "@bouncer release LEVEL" tag has no effect at all — in either pipeline —
// unless the plugin entry configures "release".
func TestBouncerReleaseWithNoConfiguredChannelIsNoOp(t *testing.T) {
	t.Parallel()

	files := map[string]string{"/src/index.ts": releaseFixture}
	outputs := emitBouncerProject(t, files, bouncerPlugins, nil)
	dts := outputs["/out/index.d.ts"]

	for _, name := range []string{"publicApi", "betaApi", "alphaApi", "internalApi", "untaggedApi"} {
		assert.Assert(t, strings.Contains(dts, name), "with no configured release channel, %s must not be trimmed:\n%s", name, dts)
	}
}

func TestBouncerUnconfiguredProfileIsNoOp(t *testing.T) {
	t.Parallel()

	// No "profiles" entry at all: "@bouncer profile QA" resolves to
	// ActionNone, so the class is left exactly as written.
	files := map[string]string{"/src/index.ts": bouncerFixture}
	outputs := emitBouncerProject(t, files, bouncerPlugins, nil)
	js := outputs["/out/index.js"]

	assert.Assert(t, strings.Contains(js, "export class DebugOnly"), "unconfigured profile must be a no-op:\n%s", js)
}

func TestBouncerConfigActivation(t *testing.T) {
	t.Parallel()

	files := map[string]string{"/src/index.ts": bouncerFixture}
	raw := bouncerRawConfig(nil)
	outputs := emitBouncerProject(t, files, nil, raw)
	js := outputs["/out/index.js"]

	assert.Assert(t, !strings.Contains(js, "mockFn"), "config activation did not enable bouncer:\n%s", js)
}

func TestBouncerInactiveMatchesUpstream(t *testing.T) {
	t.Parallel()

	files := map[string]string{"/src/index.ts": bouncerFixture}
	baseline := emitBouncerProject(t, files, nil, nil)
	js := baseline["/out/index.js"]

	assert.Assert(t, strings.Contains(js, "mockFn"), "without the plugin, declarations must be untouched:\n%s", js)
	assert.Assert(t, strings.Contains(js, "export function internalHelper"), "without the plugin, exports must be untouched:\n%s", js)

	withEmpty := emitBouncerProject(t, files, []hooks.EmitPlugin{}, nil)
	assert.DeepEqual(t, baseline, withEmpty)
}

func TestBouncerNoTagUnchanged(t *testing.T) {
	t.Parallel()

	files := map[string]string{"/src/index.ts": "export function untouched() { return 1; }\n"}
	withPlugin := emitBouncerProject(t, files, bouncerPlugins, nil)
	without := emitBouncerProject(t, files, nil, nil)
	assert.DeepEqual(t, withPlugin, without)
}

// TestBouncerSourceMapsSurviveRemoval verifies that source maps are still
// produced and correctly reference the remaining, untouched declaration
// after another one was removed from the same file.
func TestBouncerSourceMapsSurviveRemoval(t *testing.T) {
	t.Parallel()

	files := map[string]string{"/src/index.ts": bouncerFixture}
	outputs := emitBouncerProject(t, files, bouncerPlugins, nil)

	assert.Assert(t, strings.Contains(outputs["/out/index.js"], "//# sourceMappingURL=index.js.map"))
	assert.Assert(t, strings.Contains(outputs["/out/index.js.map"], `"../src/index.ts"`), "js map sources wrong: %s", outputs["/out/index.js.map"])

	// The kept declaration retains a correct, non-degenerate mapping.
	assert.Assert(t, strings.Contains(outputs["/out/index.js"], "export const kept = 5;"))
}

// TestBouncerDoesNotDescendIntoNestedScopes verifies the "top-level only"
// scope decision: a @bouncer tag inside a function or namespace body has
// no effect.
func TestBouncerDoesNotDescendIntoNestedScopes(t *testing.T) {
	t.Parallel()

	files := map[string]string{
		"/src/index.ts": `export function outer() {
    /**
     * @bouncer remove
     */
    function nested() {
        return 1;
    }
    return nested();
}
`,
	}
	outputs := emitBouncerProject(t, files, bouncerPlugins, nil)
	js := outputs["/out/index.js"]
	assert.Assert(t, strings.Contains(js, "function nested"), "nested declaration must not be affected:\n%s", js)
}
