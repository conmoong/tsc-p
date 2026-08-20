package pathrewrite

import (
	"testing"

	"github.com/microsoft/TypeScript/tsc/internal/tspath"
	"gotest.tools/v3/assert"
)

func TestOutputExtension(t *testing.T) {
	t.Parallel()

	expected := map[string]string{
		// TypeScript implementations and declarations emit as .js.
		tspath.ExtensionTs:  ".js",
		tspath.ExtensionTsx: ".js",
		tspath.ExtensionDts: ".js",
		// ESM/CJS variants keep their module-format-specific extension.
		tspath.ExtensionMts:  ".mjs",
		tspath.ExtensionDmts: ".mjs",
		tspath.ExtensionCts:  ".cjs",
		tspath.ExtensionDcts: ".cjs",
		// Already-JavaScript sources pass through under their emitted name.
		tspath.ExtensionJs:  ".js",
		tspath.ExtensionJsx: ".js",
		tspath.ExtensionMjs: ".mjs",
		tspath.ExtensionCjs: ".cjs",
		// JSON is not transformed and must keep .json.
		tspath.ExtensionJson: ".json",
	}
	for in, want := range expected {
		got, ok := outputExtension(in)
		assert.Assert(t, ok, "expected a mapping for %q", in)
		assert.Equal(t, want, got, "mapping for %q", in)
	}

	for _, unknown := range []string{"", ".node", ".wasm", ".css", ".tsbuildinfo"} {
		_, ok := outputExtension(unknown)
		assert.Assert(t, !ok, "expected no mapping for %q", unknown)
	}
}
