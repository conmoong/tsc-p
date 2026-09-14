package paris

import (
	"os"
	"path/filepath"
	"testing"

	"gotest.tools/v3/assert"
)

func rawConfig(define map[string]any) map[string]any {
	return map[string]any{
		"compilerOptions": map[string]any{
			"plugins": []any{
				map[string]any{"name": PluginName, "define": define},
			},
		},
	}
}

func TestOptionsAbsentWhenUnconfigured(t *testing.T) {
	t.Parallel()
	assert.Assert(t, OptionsFromConfig(map[string]any{}, "/") == nil)
	assert.Assert(t, OptionsFromConfig(map[string]any{
		"compilerOptions": map[string]any{"plugins": []any{map[string]any{"name": "other"}}},
	}, "/") == nil)
}

func TestLiteralShorthandsAndValueObject(t *testing.T) {
	t.Parallel()
	options := OptionsFromConfig(rawConfig(map[string]any{
		"S":    "text",
		"N":    float64(123),
		"B":    true,
		"OBJ":  map[string]any{"value": map[string]any{"a": float64(1)}},
		"NEG":  map[string]any{"value": float64(-4.5)},
		"BOOL": map[string]any{"value": false},
	}), "/")
	assert.Assert(t, options != nil)
	assert.Equal(t, len(options.ConfigErrors), 0, "%v", options.ConfigErrors)
	assert.Equal(t, options.Values["S"].Kind, KindString)
	assert.Equal(t, options.Values["S"].Str, "text")
	assert.Equal(t, options.Values["N"].Kind, KindNumber)
	assert.Equal(t, options.Values["N"].Num, 123.0)
	assert.Equal(t, options.Values["B"].Kind, KindBool)
	assert.Equal(t, options.Values["OBJ"].Kind, KindJSON)
	assert.Equal(t, options.Values["NEG"].Num, -4.5)
	assert.Equal(t, options.Values["BOOL"].Bool, false)
}

func TestBareObjectEntryIsError(t *testing.T) {
	t.Parallel()
	options := OptionsFromConfig(rawConfig(map[string]any{
		"X": map[string]any{"a": float64(1)},
	}), "/")
	assert.Assert(t, len(options.ConfigErrors) == 1)
}

func TestEnvSource(t *testing.T) {
	t.Setenv("PARIS_TEST_SET", "from-env")
	options := OptionsFromConfig(rawConfig(map[string]any{
		"PARIS_TEST_SET":     map[string]any{"from": "env"},
		"RENAMED":            map[string]any{"from": "env", "name": "PARIS_TEST_SET"},
		"PARIS_TEST_MISSING": map[string]any{"from": "env"},
	}), "/")
	assert.Equal(t, len(options.ConfigErrors), 0, "%v", options.ConfigErrors)
	assert.Equal(t, options.Values["PARIS_TEST_SET"].Str, "from-env")
	assert.Equal(t, options.Values["RENAMED"].Str, "from-env")
	_, defined := options.Values["PARIS_TEST_MISSING"]
	assert.Assert(t, !defined, "a missing env var must be undefined, not an error")
}

func TestFileSources(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	assert.NilError(t, os.WriteFile(filepath.Join(dir, "version.txt"), []byte("1.2.3\n"), 0o644))
	assert.NilError(t, os.WriteFile(filepath.Join(dir, "data.json"), []byte(`{"port": 8080}`), 0o644))
	assert.NilError(t, os.WriteFile(filepath.Join(dir, "blob.bin"), []byte{1, 2, 255}, 0o644))
	assert.NilError(t, os.WriteFile(filepath.Join(dir, "bad.json"), []byte("{nope"), 0o644))

	options := OptionsFromConfig(rawConfig(map[string]any{
		"VERSION": map[string]any{"from": "file", "path": "version.txt"},
		"DATA":    map[string]any{"from": "file", "path": "data.json", "type": "json"},
		"U8":      map[string]any{"from": "file", "path": "blob.bin", "type": "uint8array"},
		"BUF":     map[string]any{"from": "file", "path": "blob.bin", "type": "buffer"},
	}), dir)
	assert.Equal(t, len(options.ConfigErrors), 0, "%v", options.ConfigErrors)
	assert.Equal(t, options.Values["VERSION"].Str, "1.2.3\n", "string files keep exact content")
	assert.Equal(t, options.Values["DATA"].Kind, KindJSON)
	assert.Equal(t, options.Values["U8"].Kind, KindBytes)
	assert.Equal(t, options.Values["U8"].Buffer, false)
	assert.Equal(t, options.Values["BUF"].Buffer, true)
	assert.DeepEqual(t, options.Values["BUF"].Bytes, []byte{1, 2, 255})

	for name, spec := range map[string]map[string]any{
		"missing file": {"from": "file", "path": "nope.txt"},
		"missing path": {"from": "file"},
		"bad json":     {"from": "file", "path": "bad.json", "type": "json"},
		"unknown type": {"from": "file", "path": "version.txt", "type": "yaml"},
		"unknown from": {"from": "vault"},
	} {
		options := OptionsFromConfig(rawConfig(map[string]any{"X": spec}), dir)
		assert.Assert(t, len(options.ConfigErrors) == 1, "%s should be a config error", name)
	}
}

func TestDotEnvSourceAndWildcards(t *testing.T) {
	t.Setenv("PARIS_WILD_ONE", "w1")
	t.Setenv("PARIS_WILD_TWO", "w2")
	dir := t.TempDir()
	dotEnv := "# comment\nexport MY_A=alpha\nMY_B = \"quoted value\"\nOTHER='single'\n\n"
	assert.NilError(t, os.WriteFile(filepath.Join(dir, ".env"), []byte(dotEnv), 0o644))

	options := OptionsFromConfig(rawConfig(map[string]any{
		"MY_A":         map[string]any{"from": "dot_env"},
		"RENAMED":      map[string]any{"from": "dot_env", "name": "OTHER"},
		"MY_*":         map[string]any{"from": "dot_env"},
		"PARIS_WILD_*": map[string]any{"from": "env"},
		// Exact beats wildcard regardless of map order.
		"PARIS_WILD_ONE": "overridden",
	}), dir)
	assert.Equal(t, len(options.ConfigErrors), 0, "%v", options.ConfigErrors)
	assert.Equal(t, options.Values["MY_A"].Str, "alpha")
	assert.Equal(t, options.Values["MY_B"].Str, "quoted value")
	assert.Equal(t, options.Values["RENAMED"].Str, "single")
	assert.Equal(t, options.Values["PARIS_WILD_TWO"].Str, "w2")
	assert.Equal(t, options.Values["PARIS_WILD_ONE"].Str, "overridden")

	missing := OptionsFromConfig(rawConfig(map[string]any{
		"X": map[string]any{"from": "dot_env", "path": "absent.env"},
	}), dir)
	assert.Assert(t, len(missing.ConfigErrors) == 1)

	assert.NilError(t, os.WriteFile(filepath.Join(dir, "broken.env"), []byte("NOEQUALSSIGN\n"), 0o644))
	malformed := OptionsFromConfig(rawConfig(map[string]any{
		"X": map[string]any{"from": "dot_env", "path": "broken.env"},
	}), dir)
	assert.Assert(t, len(malformed.ConfigErrors) == 1)

	badWildcard := OptionsFromConfig(rawConfig(map[string]any{
		"A_*": map[string]any{"from": "file", "path": "x"},
	}), dir)
	assert.Assert(t, len(badWildcard.ConfigErrors) == 1)
}
