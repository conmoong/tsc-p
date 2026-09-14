package pathrewrite

import (
	"testing"

	"github.com/microsoft/TypeScript/tsc/internal/collections"
	"github.com/microsoft/TypeScript/tsc/internal/core"
	"gotest.tools/v3/assert"
)

func pluginEntry(fields map[string]any) any {
	entry := map[string]any{"name": PluginName}
	for key, value := range fields {
		entry[key] = value
	}
	return map[string]any{
		"compilerOptions": map[string]any{
			"plugins": []any{entry},
		},
	}
}

func TestOptionsFromConfigAbsent(t *testing.T) {
	t.Parallel()

	assert.Assert(t, OptionsFromConfig(nil) == nil)
	assert.Assert(t, OptionsFromConfig(map[string]any{}) == nil)
	assert.Assert(t, OptionsFromConfig(map[string]any{"compilerOptions": map[string]any{}}) == nil)
	// A plugins array with only other (language-service) entries.
	assert.Assert(t, OptionsFromConfig(map[string]any{
		"compilerOptions": map[string]any{
			"plugins": []any{map[string]any{"name": "typescript-styled-plugin"}},
		},
	}) == nil)
}

func TestOptionsFromConfigDefaults(t *testing.T) {
	t.Parallel()

	options := OptionsFromConfig(pluginEntry(nil))
	assert.Assert(t, options != nil)
	assert.Equal(t, true, options.Extension)
	assert.Equal(t, true, options.Declarations)
	assert.Equal(t, true, options.aliasEnabled)
	assert.Equal(t, 0, len(options.aliasRules))
}

func TestOptionsFromConfigOrderedMaps(t *testing.T) {
	t.Parallel()

	// The real tsconfig parser produces OrderedMaps, not plain maps.
	entry := &collections.OrderedMap[string, any]{}
	entry.Set("name", PluginName)
	entry.Set("extension", false)
	aliases := &collections.OrderedMap[string, any]{}
	aliases.Set("@keep/*", false)
	entry.Set("alias", aliases)
	compilerOptions := &collections.OrderedMap[string, any]{}
	compilerOptions.Set("plugins", []any{entry})
	raw := &collections.OrderedMap[string, any]{}
	raw.Set("compilerOptions", compilerOptions)

	options := OptionsFromConfig(raw)
	assert.Assert(t, options != nil)
	assert.Equal(t, false, options.Extension)
	assert.Equal(t, 1, len(options.aliasRules))

	rewrite, _ := options.decide("@keep/thing")
	assert.Equal(t, false, rewrite)
}

func TestOptionsFromConfigFields(t *testing.T) {
	t.Parallel()

	options := OptionsFromConfig(pluginEntry(map[string]any{
		"extension":    false,
		"declarations": false,
		"alias": map[string]any{
			"@keep/*":  false,
			"@exact":   true,
			"@web/*":   map[string]any{"enabled": true, "extension": false},
			"./*":      map[string]any{"extension": true},
			"bad**pat": false, // invalid pattern: ignored
		},
	}))
	assert.Assert(t, options != nil)
	assert.Equal(t, false, options.Extension)
	assert.Equal(t, false, options.Declarations)
	assert.Equal(t, 4, len(options.aliasRules), "invalid patterns must be skipped")
}

func TestDecideRelative(t *testing.T) {
	t.Parallel()

	// With no rules, relative specifiers follow the blanket setting and
	// global extension, exactly like any other specifier.
	options := defaultOptions()
	rewrite, withExtension := options.decide("./util")
	assert.Assert(t, rewrite && withExtension)

	options.Extension = false
	rewrite, withExtension = options.decide("../util")
	assert.Assert(t, rewrite && !withExtension)

	// "./*" and "../*" rules address relative imports like any pattern:
	// per-rule extension override beats the global setting, and "./*" does
	// not match parent-relative specifiers.
	withExt := true
	options.aliasRules = []aliasRule{
		{pattern: mustPattern(t, "./*"), enabled: true, extension: &withExt},
		{pattern: mustPattern(t, "../*"), enabled: false},
	}
	rewrite, withExtension = options.decide("./util")
	assert.Assert(t, rewrite && withExtension)
	rewrite, _ = options.decide("../util")
	assert.Assert(t, !rewrite)
}

func TestDecideAliasRules(t *testing.T) {
	t.Parallel()

	noExt := false
	options := defaultOptions()
	options.aliasRules = []aliasRule{
		{pattern: mustPattern(t, "@keep/*"), enabled: false},
		{pattern: mustPattern(t, "@keep/but-this"), enabled: true},
		{pattern: mustPattern(t, "@web/*"), enabled: true, extension: &noExt},
	}

	// Unmatched aliases default to enabled with the global extension.
	rewrite, withExtension := options.decide("@app/anything")
	assert.Assert(t, rewrite && withExtension)

	// Star match disables.
	rewrite, _ = options.decide("@keep/thing")
	assert.Assert(t, !rewrite)

	// Exact match beats the star match.
	rewrite, _ = options.decide("@keep/but-this")
	assert.Assert(t, rewrite)

	// Per-alias extension override.
	rewrite, withExtension = options.decide("@web/component")
	assert.Assert(t, rewrite && !withExtension)
}

func TestDecideWhitelistIdiom(t *testing.T) {
	t.Parallel()

	// {"*": false, "@app/*": true} — the longer prefix outranks bare "*".
	options := defaultOptions()
	options.aliasRules = []aliasRule{
		{pattern: mustPattern(t, "*"), enabled: false},
		{pattern: mustPattern(t, "@app/*"), enabled: true},
	}

	rewrite, _ := options.decide("@app/thing")
	assert.Assert(t, rewrite)
	rewrite, _ = options.decide("@other/thing")
	assert.Assert(t, !rewrite)
	rewrite, _ = options.decide("anything")
	assert.Assert(t, !rewrite)
}

func TestDecideBlanketAliasOff(t *testing.T) {
	t.Parallel()

	// alias: false blankets every unmatched specifier, relative included;
	// "./*"/"../*" rules can selectively re-enable relative imports.
	options := defaultOptions()
	options.aliasEnabled = false

	rewrite, _ := options.decide("@app/thing")
	assert.Assert(t, !rewrite)
	rewrite, _ = options.decide("./util")
	assert.Assert(t, !rewrite)

	options.aliasRules = []aliasRule{
		{pattern: mustPattern(t, "./*"), enabled: true},
	}
	rewrite, _ = options.decide("./util")
	assert.Assert(t, rewrite)
	rewrite, _ = options.decide("@app/thing")
	assert.Assert(t, !rewrite)
}

func mustPattern(t *testing.T, text string) core.Pattern {
	t.Helper()
	pattern := core.TryParsePattern(text)
	assert.Assert(t, pattern.IsValid(), "invalid test pattern %q", text)
	return pattern
}
