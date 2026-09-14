package bouncer

import (
	"testing"

	"github.com/microsoft/TypeScript/tsc/internal/collections"
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
	assert.Assert(t, OptionsFromConfig(map[string]any{
		"compilerOptions": map[string]any{
			"plugins": []any{map[string]any{"name": "some-other-plugin"}},
		},
	}) == nil)
}

func TestOptionsFromConfigNoProfiles(t *testing.T) {
	t.Parallel()

	options := OptionsFromConfig(pluginEntry(nil))
	assert.Assert(t, options != nil)
	assert.Assert(t, len(options.Profiles) == 0)
	assert.Equal(t, -1, options.ReleaseRank, "no configured release channel disables trimming")
}

func TestOptionsFromConfigRelease(t *testing.T) {
	t.Parallel()

	cases := []struct {
		channel  any
		wantRank int
	}{
		{"public", 0},
		{"beta", 1},
		{"alpha", 2},
		{"internal", 3},
		{"not-a-channel", -1}, // unrecognised: trimming stays disabled
		{42, -1},              // wrong type: trimming stays disabled
	}
	for _, c := range cases {
		options := OptionsFromConfig(pluginEntry(map[string]any{"release": c.channel}))
		assert.Assert(t, options != nil)
		assert.Equal(t, c.wantRank, options.ReleaseRank, "release=%v", c.channel)
	}
}

func TestOptionsFromConfigProfileRelease(t *testing.T) {
	t.Parallel()

	options := OptionsFromConfig(pluginEntry(map[string]any{
		"profiles": map[string]any{
			"internal-only": map[string]any{"release": "internal"},
			"bad-channel":   map[string]any{"release": "nope"},
			"bad-type":      map[string]any{"release": 1},
		},
	}))
	assert.Assert(t, options != nil)
	assert.Equal(t, Action{Kind: ActionRelease, Name: "internal"}, options.Profiles["internal-only"])
	for _, name := range []string{"bad-channel", "bad-type"} {
		_, has := options.Profiles[name]
		assert.Assert(t, !has, "profile %q should have been rejected", name)
	}
}

func TestOptionsFromConfigProfiles(t *testing.T) {
	t.Parallel()

	options := OptionsFromConfig(pluginEntry(map[string]any{
		"profiles": map[string]any{
			"ABC":         "remove",
			"visible":     "export",
			"hidden":      "no-export",
			"as-default":  "export-default",
			"renamed":     map[string]any{"export-as": "NEW_NAME"},
			"bad-str":     "not-a-real-action", // unrecognised string: ignored
			"bad-obj":     map[string]any{},    // no "export-as" key: ignored
			"bad-obj-num": map[string]any{"export-as": 42},
			"bad-num":     42, // wrong type entirely: ignored
			"beta-only":   map[string]any{"release": "beta"},
		},
	}))
	assert.Assert(t, options != nil)
	assert.Equal(t, Action{Kind: ActionRemove}, options.Profiles["ABC"])
	assert.Equal(t, Action{Kind: ActionExport}, options.Profiles["visible"])
	assert.Equal(t, Action{Kind: ActionNoExport}, options.Profiles["hidden"])
	assert.Equal(t, Action{Kind: ActionExportDefault}, options.Profiles["as-default"])
	assert.Equal(t, Action{Kind: ActionExportAs, Name: "NEW_NAME"}, options.Profiles["renamed"])
	assert.Equal(t, Action{Kind: ActionRelease, Name: "beta"}, options.Profiles["beta-only"])

	for _, name := range []string{"bad-str", "bad-obj", "bad-obj-num", "bad-num"} {
		_, has := options.Profiles[name]
		assert.Assert(t, !has, "profile %q should have been rejected", name)
	}
}

func TestOptionsFromConfigOrderedMaps(t *testing.T) {
	t.Parallel()

	// The real tsconfig parser produces OrderedMaps, not plain maps.
	profiles := &collections.OrderedMap[string, any]{}
	profiles.Set("ABC", "remove")
	renamed := &collections.OrderedMap[string, any]{}
	renamed.Set("export-as", "NEW_NAME")
	profiles.Set("renamed", renamed)
	entry := &collections.OrderedMap[string, any]{}
	entry.Set("name", PluginName)
	entry.Set("profiles", profiles)
	compilerOptions := &collections.OrderedMap[string, any]{}
	compilerOptions.Set("plugins", []any{entry})
	raw := &collections.OrderedMap[string, any]{}
	raw.Set("compilerOptions", compilerOptions)

	options := OptionsFromConfig(raw)
	assert.Assert(t, options != nil)
	assert.Equal(t, Action{Kind: ActionRemove}, options.Profiles["ABC"])
	assert.Equal(t, Action{Kind: ActionExportAs, Name: "NEW_NAME"}, options.Profiles["renamed"])
}
