package hooks

import "github.com/microsoft/TypeScript/tsc/internal/collections"

// ConfigGet reads a key from a parsed-JSON object, which the tsconfig
// parser represents as *collections.OrderedMap[string, any] (plain
// map[string]any is also accepted, for tests and other callers that build
// raw config values directly).
func ConfigGet(value any, key string) (any, bool) {
	switch object := value.(type) {
	case *collections.OrderedMap[string, any]:
		return object.Get(key)
	case map[string]any:
		result, ok := object[key]
		return result, ok
	default:
		return nil, false
	}
}

// ConfigEach iterates a parsed-JSON object's entries.
func ConfigEach(value any, callback func(key string, value any)) {
	switch object := value.(type) {
	case *collections.OrderedMap[string, any]:
		for key, entryValue := range object.Entries() {
			callback(key, entryValue)
		}
	case map[string]any:
		for key, entryValue := range object {
			callback(key, entryValue)
		}
	}
}

// FindPluginEntry locates the compilerOptions.plugins array entry with the
// given name in a raw parsed tsconfig (tsoptions.ParsedCommandLine.Raw).
// compilerOptions.plugins is upstream's language-service plugin list;
// entries with other names belong to other plugins (editor or otherwise)
// and are not touched. Returns the entry (so the caller can read its own
// fields) and whether one was found.
func FindPluginEntry(raw any, name string) (any, bool) {
	compilerOptions, ok := ConfigGet(raw, "compilerOptions")
	if !ok {
		return nil, false
	}
	pluginsValue, ok := ConfigGet(compilerOptions, "plugins")
	if !ok {
		return nil, false
	}
	plugins, ok := pluginsValue.([]any)
	if !ok {
		return nil, false
	}
	for _, entry := range plugins {
		if entryName, ok := ConfigGet(entry, "name"); ok && entryName == name {
			return entry, true
		}
	}
	return nil, false
}
