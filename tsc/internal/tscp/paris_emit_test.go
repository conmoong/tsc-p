// Integration tests for the @conmoong/paris plugin through the real
// compiler emit pipeline: condition freezing, nesting with unevaluated
// pruning, value substitution, runtime-import elision, and — because paris
// is deliberately explicit-and-loud — the build-failing diagnostics for
// every defined misuse.

package tscp_test

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/microsoft/TypeScript/tsc/internal/ast"
	"github.com/microsoft/TypeScript/tsc/internal/bundled"
	"github.com/microsoft/TypeScript/tsc/internal/compiler"
	"github.com/microsoft/TypeScript/tsc/internal/core"
	"github.com/microsoft/TypeScript/tsc/internal/tscp/hooks"
	"github.com/microsoft/TypeScript/tsc/internal/tscp/paris"
	"github.com/microsoft/TypeScript/tsc/internal/tsoptions"
	"github.com/microsoft/TypeScript/tsc/internal/vfs/vfstest"
	"gotest.tools/v3/assert"
)

// parisRuntimeStub gives the fixture a resolvable, typed @conmoong/paris
// module, mirroring the published runtime package's surface.
var parisRuntimeStub = map[string]string{
	"/src/node_modules/@conmoong/paris/package.json": `{"name": "@conmoong/paris", "version": "0.0.0", "main": "./index.js", "types": "./index.d.ts"}`,
	"/src/node_modules/@conmoong/paris/index.js":     "module.exports = {};\n",
	"/src/node_modules/@conmoong/paris/index.d.ts": `type Body = () => void;
export declare function ifDef(name: string, body: Body): void;
export declare function ifNotDef(name: string, body: Body): void;
export declare function ifEq(name: string, expected: string | number | boolean, body: Body): void;
export declare function ifNotEq(name: string, expected: string | number | boolean, body: Body): void;
export declare function ifMatch(name: string, pattern: RegExp, body: Body): void;
export declare function ifNotMatch(name: string, pattern: RegExp, body: Body): void;
export declare function ifGt(name: string, bound: number, body: Body): void;
export declare function ifGte(name: string, bound: number, body: Body): void;
export declare function ifLt(name: string, bound: number, body: Body): void;
export declare function ifLte(name: string, bound: number, body: Body): void;
export declare function ifTrue(name: string, body: Body): void;
export declare function ifFalse(name: string, body: Body): void;
export declare function ifTruthy(name: string, body: Body): void;
export declare function ifNotTruthy(name: string, body: Body): void;
export declare function value<T = string>(name: string): T;
declare const paris: {
    ifDef: typeof ifDef;
    ifNotDef: typeof ifNotDef;
    ifEq: typeof ifEq;
    ifNotEq: typeof ifNotEq;
    ifMatch: typeof ifMatch;
    ifNotMatch: typeof ifNotMatch;
    ifGt: typeof ifGt;
    ifGte: typeof ifGte;
    ifLt: typeof ifLt;
    ifLte: typeof ifLte;
    ifTrue: typeof ifTrue;
    ifFalse: typeof ifFalse;
    ifTruthy: typeof ifTruthy;
    ifNotTruthy: typeof ifNotTruthy;
    value: typeof value;
};
export default paris;
`,
}

func parisPlugin(t *testing.T, define map[string]any) hooks.EmitPlugin {
	t.Helper()
	raw := map[string]any{
		"compilerOptions": map[string]any{
			"plugins": []any{map[string]any{"name": paris.PluginName, "define": define}},
		},
	}
	options := paris.OptionsFromConfig(raw, "/src")
	assert.Assert(t, options != nil)
	return paris.NewPlugin(options)
}

// emitParis compiles sources (plus the runtime stub) with the plugin and
// returns outputs and emit diagnostics.
func emitParis(t *testing.T, sources map[string]string, define map[string]any) (map[string]string, []*ast.Diagnostic) {
	t.Helper()
	if !bundled.Embedded {
		t.Skip("bundled files are not embedded")
	}

	files := make(map[string]string, len(sources)+len(parisRuntimeStub))
	for name, content := range parisRuntimeStub {
		files[name] = content
	}
	fileNames := make([]string, 0, len(sources))
	for name, content := range sources {
		files[name] = content
		fileNames = append(fileNames, name)
	}

	fs := vfstest.FromMap(files, true /*useCaseSensitiveFileNames*/)
	fs = bundled.WrapFS(fs)

	var host compiler.CompilerHost = compiler.NewCompilerHost("/src", fs, bundled.LibPath(), nil, nil)
	host = &pluginCompilerHost{CompilerHost: host, plugins: []hooks.EmitPlugin{parisPlugin(t, define)}}

	options := baseOptions()
	options.Module = core.ModuleKindESNext

	program := compiler.NewProgram(compiler.ProgramOptions{
		Config: &tsoptions.ParsedCommandLine{
			ParsedConfig: &core.ParsedOptions{
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

// diagnosticText renders a diagnostic's key and arguments; the paris
// message carries its whole text as the single argument.
func diagnosticText(diagnostic *ast.Diagnostic) string {
	return string(diagnostic.MessageKey()) + " " + strings.Join(diagnostic.MessageArgs(), " ")
}

func assertNoDiagnostics(t *testing.T, diagnostics []*ast.Diagnostic) {
	t.Helper()
	for _, diagnostic := range diagnostics {
		t.Errorf("unexpected diagnostic: %s", diagnosticText(diagnostic))
	}
}

func assertDiagnosticContains(t *testing.T, diagnostics []*ast.Diagnostic, substring string) {
	t.Helper()
	texts := make([]string, 0, len(diagnostics))
	for _, diagnostic := range diagnostics {
		text := diagnosticText(diagnostic)
		if strings.Contains(text, substring) {
			return
		}
		texts = append(texts, text)
	}
	t.Errorf("no diagnostic contains %q; got %d diagnostics: %v", substring, len(texts), texts)
}

func TestParisTrueEmitsBlockAndElidesImport(t *testing.T) {
	t.Parallel()
	outputs, diagnostics := emitParis(t, map[string]string{
		"/src/index.ts": `import { ifDef } from "@conmoong/paris";
export const before = 1;
ifDef("DEBUG", () => {
    const tracer = "on";
    console.log(tracer);
});
`,
	}, map[string]any{"DEBUG": "1"})
	assertNoDiagnostics(t, diagnostics)
	js := outputs["/out/index.js"]
	assert.Assert(t, strings.Contains(js, `const tracer = "on"`), "body lost:\n%s", js)
	assert.Assert(t, !strings.Contains(js, "ifDef"), "condition call survived:\n%s", js)
	assert.Assert(t, !strings.Contains(js, "@conmoong/paris"), "runtime import survived:\n%s", js)
	assert.Assert(t, strings.Contains(js, "{"), "body must be a block statement:\n%s", js)
	dts := outputs["/out/index.d.ts"]
	assert.Assert(t, !strings.Contains(dts, "@conmoong/paris"), "declaration output references the runtime:\n%s", dts)
}

func TestParisFalsePrunesUnevaluated(t *testing.T) {
	t.Parallel()
	outputs, diagnostics := emitParis(t, map[string]string{
		"/src/index.ts": `import { ifDef, ifEq } from "@conmoong/paris";
ifDef("MISSING", () => {
    // UNDEFINED_TOO would be a build error if evaluated; a pruned branch
    // must never evaluate its inner conditions (#ifdef-guard semantics).
    ifEq("UNDEFINED_TOO", "x", () => {
        console.log("never");
    });
});
export const after = 2;
`,
	}, map[string]any{"DEBUG": "1"})
	assertNoDiagnostics(t, diagnostics)
	js := outputs["/out/index.js"]
	assert.Assert(t, !strings.Contains(js, "never"), "pruned branch leaked:\n%s", js)
	assert.Assert(t, !strings.Contains(js, "@conmoong/paris"), "runtime import survived:\n%s", js)
	assert.Assert(t, strings.Contains(js, "const after = 2"), "surrounding code lost:\n%s", js)
}

func TestParisNestingEvaluatesSurvivingBodies(t *testing.T) {
	t.Parallel()
	outputs, diagnostics := emitParis(t, map[string]string{
		"/src/index.ts": `import { ifDef, ifEq, ifGt } from "@conmoong/paris";
ifDef("MODE", () => {
    ifEq("MODE", "prod", () => {
        console.log("prod-path");
    });
    ifGt("LEVEL", 3, () => {
        console.log("verbose-path");
    });
});
export {};
`,
	}, map[string]any{"MODE": "prod", "LEVEL": float64(2)})
	assertNoDiagnostics(t, diagnostics)
	js := outputs["/out/index.js"]
	assert.Assert(t, strings.Contains(js, "prod-path"), "true inner branch lost:\n%s", js)
	assert.Assert(t, !strings.Contains(js, "verbose-path"), "false inner branch kept:\n%s", js)
}

func TestParisDefaultAndNamespaceImports(t *testing.T) {
	t.Parallel()
	outputs, diagnostics := emitParis(t, map[string]string{
		"/src/index.ts": `import paris from "@conmoong/paris";
import * as p from "@conmoong/paris";
paris.ifTruthy("FLAG", () => {
    console.log("via-default");
});
p.ifNotTruthy("FLAG", () => {
    console.log("via-namespace");
});
export {};
`,
	}, map[string]any{"FLAG": "yes"})
	assertNoDiagnostics(t, diagnostics)
	js := outputs["/out/index.js"]
	assert.Assert(t, strings.Contains(js, "via-default"), "default-import call not transformed:\n%s", js)
	assert.Assert(t, !strings.Contains(js, "via-namespace"), "negated call kept its body:\n%s", js)
	assert.Assert(t, !strings.Contains(js, "@conmoong/paris"), "runtime imports survived:\n%s", js)
}

// TestParisRequireBindings verifies that TypeScript source using CommonJS
// require() to reach @conmoong/paris — a whole-module binding and a
// destructured, partly-renamed binding — is recognised exactly like an ESM
// import: conditions are frozen, and the require() is elided once every
// bound name has been transformed away.
func TestParisRequireBindings(t *testing.T) {
	t.Parallel()
	outputs, diagnostics := emitParis(t, map[string]string{
		"/src/globals.d.ts": "declare function require(id: string): any;\n",
		"/src/index.ts": `const paris = require("@conmoong/paris");
const { ifTruthy, ifNotTruthy: notTruthy } = require("@conmoong/paris");
paris.ifDef("FLAG", () => {
    console.log("via-namespace-require");
});
ifTruthy("FLAG", () => {
    console.log("via-named-require");
});
notTruthy("FLAG", () => {
    console.log("via-renamed-require");
});
export {};
`,
	}, map[string]any{"FLAG": "yes"})
	assertNoDiagnostics(t, diagnostics)
	js := outputs["/out/index.js"]
	assert.Assert(t, strings.Contains(js, "via-namespace-require"), "namespace-style require call not transformed:\n%s", js)
	assert.Assert(t, strings.Contains(js, "via-named-require"), "named require call not transformed:\n%s", js)
	assert.Assert(t, !strings.Contains(js, "via-renamed-require"), "negated renamed-binding call kept its body:\n%s", js)
	assert.Assert(t, !strings.Contains(js, "require(") && !strings.Contains(js, "@conmoong/paris"), "require() bindings survived:\n%s", js)
}

// TestParisRequireMultiDeclaratorStatementKeptIntact verifies the
// deliberately conservative safety rule: a require() binding sharing a
// variable statement with an unrelated declarator is never removed, even
// once fully unused, to avoid partial-statement surgery.
func TestParisRequireMultiDeclaratorStatementKeptIntact(t *testing.T) {
	t.Parallel()
	outputs, diagnostics := emitParis(t, map[string]string{
		"/src/globals.d.ts": "declare function require(id: string): any;\n",
		"/src/index.ts": `const paris = require("@conmoong/paris"), unrelated = 1;
paris.ifDef("FLAG", () => {
    console.log("ran");
});
console.log(unrelated);
`,
	}, map[string]any{"FLAG": "yes"})
	assertNoDiagnostics(t, diagnostics)
	js := outputs["/out/index.js"]
	assert.Assert(t, strings.Contains(js, "ran"), "condition body lost:\n%s", js)
	assert.Assert(t, strings.Contains(js, `require("@conmoong/paris")`), "multi-declarator require statement was incorrectly removed:\n%s", js)
}

func TestParisValueSubstitution(t *testing.T) {
	t.Parallel()
	outputs, diagnostics := emitParis(t, map[string]string{
		"/src/index.ts": `import { value } from "@conmoong/paris";
export const version = value("VERSION");
export const port = value<number>("PORT");
export const flag = value<boolean>("FLAG");
export const config = value<object>("CONFIG");
`,
	}, map[string]any{
		"VERSION": "1.2.3",
		"PORT":    float64(8080),
		"FLAG":    true,
		"CONFIG":  map[string]any{"value": map[string]any{"retries": float64(3), "name": "svc"}},
	})
	assertNoDiagnostics(t, diagnostics)
	js := outputs["/out/index.js"]
	assert.Assert(t, strings.Contains(js, `const version = "1.2.3"`), "string value:\n%s", js)
	assert.Assert(t, strings.Contains(js, "const port = 8080"), "number value:\n%s", js)
	assert.Assert(t, strings.Contains(js, "const flag = true"), "boolean value:\n%s", js)
	assert.Assert(t, strings.Contains(js, `"retries": 3`), "json object value:\n%s", js)
	assert.Assert(t, strings.Contains(js, `"name": "svc"`), "json object value:\n%s", js)
	assert.Assert(t, !strings.Contains(js, "@conmoong/paris"), "runtime import survived:\n%s", js)
}

func TestParisMatchWithFlags(t *testing.T) {
	t.Parallel()
	outputs, diagnostics := emitParis(t, map[string]string{
		"/src/index.ts": `import { ifMatch } from "@conmoong/paris";
ifMatch("STAGE", /^prod/i, () => {
    console.log("matched");
});
export {};
`,
	}, map[string]any{"STAGE": "Production"})
	assertNoDiagnostics(t, diagnostics)
	assert.Assert(t, strings.Contains(outputs["/out/index.js"], "matched"))
}

func TestParisInactiveFileIsUntouched(t *testing.T) {
	t.Parallel()
	source := map[string]string{
		"/src/index.ts": "export const untouched = 1;\n",
	}
	withPlugin, diagnostics := emitParis(t, source, map[string]any{"DEBUG": "1"})
	assertNoDiagnostics(t, diagnostics)
	withoutPlugin := emitFixture(t, map[string]string{"/src/index.ts": source["/src/index.ts"]}, func() *core.CompilerOptions {
		options := baseOptions()
		options.Module = core.ModuleKindESNext
		return options
	}(), nil)
	assert.Equal(t, withPlugin["/out/index.js"], withoutPlugin["/out/index.js"])
	assert.Equal(t, withPlugin["/out/index.d.ts"], withoutPlugin["/out/index.d.ts"])
}

func TestParisMisuseDiagnostics(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		source   string
		expected string
	}{
		{
			"expression position",
			`import { ifDef } from "@conmoong/paris";
export const x = ifDef("DEBUG", () => {});
`,
			"standalone statement",
		},
		{
			"return in body",
			`import { ifDef } from "@conmoong/paris";
export function f(): void {
    ifDef("DEBUG", () => {
        return;
    });
}
`,
			"must not contain return",
		},
		{
			"var in body",
			`import { ifDef } from "@conmoong/paris";
ifDef("DEBUG", () => {
    var leaky = 1;
    console.log(leaky);
});
export {};
`,
			"must not declare var",
		},
		{
			"non-literal variable name",
			`import { ifDef } from "@conmoong/paris";
const name = "DEBUG";
ifDef(name, () => {});
export {};
`,
			"string literal",
		},
		{
			"undefined variable in evaluated check",
			`import { ifEq } from "@conmoong/paris";
ifEq("NOT_CONFIGURED", "x", () => {});
export {};
`,
			"not defined",
		},
		{
			"ifTrue on a string value",
			`import { ifTrue } from "@conmoong/paris";
ifTrue("DEBUG", () => {});
export {};
`,
			"strictly boolean",
		},
		{
			"JS-only regex",
			`import { ifMatch } from "@conmoong/paris";
ifMatch("DEBUG", /(?=x)/, () => {});
export {};
`,
			"RE2",
		},
		{
			"non-inline callback",
			`import { ifDef } from "@conmoong/paris";
const body = () => {};
ifDef("DEBUG", body);
export {};
`,
			"inline function",
		},
		{
			"async callback",
			`import { ifDef } from "@conmoong/paris";
ifDef("DEBUG", async () => {});
export {};
`,
			"async",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			outputs, diagnostics := emitParis(t, map[string]string{"/src/index.ts": testCase.source}, map[string]any{"DEBUG": "1"})
			assertDiagnosticContains(t, diagnostics, testCase.expected)
			// The erroring statement is left as written — never half-transformed.
			assert.Assert(t, len(outputs) > 0)
		})
	}
}

func TestParisConfigErrorFailsBuild(t *testing.T) {
	t.Parallel()
	_, diagnostics := emitParis(t, map[string]string{
		"/src/index.ts": "export const x = 1;\n",
	}, map[string]any{
		"BROKEN": map[string]any{"from": "file", "path": "does-not-exist.txt"},
	})
	assertDiagnosticContains(t, diagnostics, "cannot read file")
}
