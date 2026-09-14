// Integration tests for the @conmoong/pure plugin through the real emit
// pipeline: /*#__PURE__*/ annotation of @pure-tagged top-level variable
// initialisers, spine-only scope, survival through CommonJS lowering, and
// byte-parity for untagged code.

package tscp_test

import (
	"strings"
	"testing"

	"github.com/microsoft/TypeScript/tsc/internal/core"
	"github.com/microsoft/TypeScript/tsc/internal/tscp/hooks"
	"github.com/microsoft/TypeScript/tsc/internal/tscp/pure"
	"gotest.tools/v3/assert"
)

func purePlugin() hooks.EmitPlugin {
	return pure.NewPlugin(&pure.Options{})
}

func TestPureAnnotatesTaggedInitializers(t *testing.T) {
	t.Parallel()
	files := map[string]string{
		"/src/index.ts": `function createThing(n: number): object { return { n }; }
/** @pure */
export const thing = createThing(1);
/** @pure */
export const instance = new Map<string, number>(), second = createThing(2);
export const untouched = createThing(3);
`,
	}
	for _, moduleKind := range []core.ModuleKind{core.ModuleKindESNext, core.ModuleKindCommonJS} {
		options := baseOptions()
		options.Module = moduleKind
		outputs := emitFixture(t, files, options, []hooks.EmitPlugin{purePlugin()})
		js := outputs["/out/index.js"]
		assert.Assert(t, strings.Contains(js, "/*#__PURE__*/ createThing(1)"), "tagged call not annotated (%v):\n%s", moduleKind, js)
		assert.Assert(t, strings.Contains(js, "/*#__PURE__*/ new Map()"), "tagged new expression not annotated (%v):\n%s", moduleKind, js)
		assert.Assert(t, strings.Contains(js, "/*#__PURE__*/ createThing(2)"), "second declarator not annotated (%v):\n%s", moduleKind, js)
		assert.Assert(t, strings.Contains(js, "untouched = createThing(3)") && !strings.Contains(js, "/*#__PURE__*/ createThing(3)"),
			"untagged statement must stay unannotated (%v):\n%s", moduleKind, js)
	}
}

func TestPureSkipsFunctionBodies(t *testing.T) {
	t.Parallel()
	files := map[string]string{
		"/src/index.ts": `function make(f: () => number): () => number { return f; }
function inner(): number { return 1; }
/** @pure */
export const lazy = make(() => inner());
`,
	}
	options := baseOptions()
	options.Module = core.ModuleKindESNext
	outputs := emitFixture(t, files, options, []hooks.EmitPlugin{purePlugin()})
	js := outputs["/out/index.js"]
	assert.Assert(t, strings.Contains(js, "/*#__PURE__*/ make("), "spine call not annotated:\n%s", js)
	assert.Assert(t, !strings.Contains(js, "/*#__PURE__*/ inner()"), "call inside a function body must not be annotated:\n%s", js)
}

func TestPureUntaggedFileIsByteIdentical(t *testing.T) {
	t.Parallel()
	files := map[string]string{
		"/src/index.ts": "export const value = [1, 2].map(n => n * 2);\n",
	}
	options := baseOptions()
	options.Module = core.ModuleKindESNext
	with := emitFixture(t, files, options, []hooks.EmitPlugin{purePlugin()})
	without := emitFixture(t, files, options, nil)
	assert.DeepEqual(t, with, without)
}
