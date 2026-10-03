package graph

import (
	"os"
	"path/filepath"
	"testing"

	"gotest.tools/v3/assert"
)

func rawConfig(entry map[string]any) any {
	full := map[string]any{"name": PluginName}
	for k, v := range entry {
		full[k] = v
	}
	return map[string]any{
		"compilerOptions": map[string]any{
			"plugins": []any{full},
		},
	}
}

func TestOptionsAbsentWhenUnconfigured(t *testing.T) {
	t.Parallel()
	assert.Assert(t, OptionsFromConfig(map[string]any{}, "/", "") == nil)
}

func TestEmitBoolDefaultPath(t *testing.T) {
	t.Parallel()
	options := OptionsFromConfig(rawConfig(map[string]any{"emit": true}), "/src", "/src/tsconfig.json")
	assert.Assert(t, options != nil)
	assert.Assert(t, options.EmitEnabled)
	assert.Equal(t, filepath.Join("/src", "tsconfig.graph.json"), options.EmitPath)
}

func TestEmitStringCustomPath(t *testing.T) {
	t.Parallel()
	options := OptionsFromConfig(rawConfig(map[string]any{"emit": "out/graph.json"}), "/src", "/src/tsconfig.json")
	assert.Assert(t, options != nil)
	assert.Assert(t, options.EmitEnabled)
	assert.Equal(t, filepath.Join("/src", "out/graph.json"), options.EmitPath)
}

func TestEmitFalseDisabled(t *testing.T) {
	t.Parallel()
	options := OptionsFromConfig(rawConfig(map[string]any{"emit": false}), "/src", "/src/tsconfig.json")
	assert.Assert(t, options != nil)
	assert.Assert(t, !options.EmitEnabled)
}

func TestDefaultEmitPathUsesTsconfigBasename(t *testing.T) {
	t.Parallel()
	options := OptionsFromConfig(rawConfig(map[string]any{"emit": true}), "/pkg", "/pkg/tsconfig.build.json")
	assert.Assert(t, options != nil)
	assert.Equal(t, filepath.Join("/pkg", "tsconfig.build.graph.json"), options.EmitPath)
}

func TestRuleParsingAndMerging(t *testing.T) {
	t.Parallel()
	options := OptionsFromConfig(rawConfig(map[string]any{
		"rules": []any{
			map[string]any{"module": "./*", "cycle": "error"},
			map[string]any{"module": "some_npm_library", "phantomImport": "warning"},
			// Same pattern as the first entry: merges, later field wins.
			map[string]any{"module": "./*", "cycle": "warning", "unused": "error"},
			map[string]any{"module": "./src/features/*", "importRules": []any{
				map[string]any{"module": "./src/other-features/*", "severity": "error"},
			}},
		},
	}), "/", "/tsconfig.json")
	assert.Assert(t, options != nil)
	assert.Equal(t, 3, len(options.Rules), "same-pattern entries must merge into one")

	var star, npm, features *Rule
	for i := range options.Rules {
		switch options.Rules[i].PatternText {
		case "./*":
			star = &options.Rules[i]
		case "some_npm_library":
			npm = &options.Rules[i]
		case "./src/features/*":
			features = &options.Rules[i]
		}
	}
	assert.Assert(t, star != nil && npm != nil && features != nil)
	assert.Assert(t, star.Cycle != nil && *star.Cycle == SeverityWarning, "later entry's cycle value must win the merge")
	assert.Assert(t, star.Unused != nil && *star.Unused == SeverityError)
	assert.Assert(t, npm.PhantomImport != nil && *npm.PhantomImport == SeverityWarning)
	assert.Equal(t, 1, len(features.ImportRules))
	assert.Equal(t, SeverityError, features.ImportRules[0].Severity)
}

func TestRuleWithInvalidPatternIsSkipped(t *testing.T) {
	t.Parallel()
	options := OptionsFromConfig(rawConfig(map[string]any{
		"rules": []any{
			map[string]any{"module": "**", "cycle": "error"}, // two stars: invalid
			map[string]any{"module": "./*", "cycle": "error"},
		},
	}), "/", "/tsconfig.json")
	assert.Assert(t, options != nil)
	assert.Equal(t, 1, len(options.Rules))
}

func TestLoadNearestPackageJson(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	assert.NilError(t, os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{
        "name": "@myorg/pkg",
        "dependencies": { "lodash": "^4.0.0" },
        "devDependencies": { "typescript": "^5.0.0" }
    }`), 0o644))
	sub := filepath.Join(dir, "src", "nested")
	assert.NilError(t, os.MkdirAll(sub, 0o755))

	deps, devDeps, name := loadNearestPackageJson(sub)
	assert.Equal(t, "@myorg/pkg", name)
	assert.Assert(t, deps["lodash"])
	assert.Assert(t, devDeps["typescript"])
	assert.Assert(t, !deps["typescript"])
}

func TestLoadNearestPackageJsonMissing(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	deps, devDeps, name := loadNearestPackageJson(dir)
	assert.Assert(t, deps == nil)
	assert.Assert(t, devDeps == nil)
	assert.Equal(t, "", name)
}
