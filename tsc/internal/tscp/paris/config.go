package paris

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/microsoft/TypeScript/tsc/internal/tscp/hooks"
	"github.com/microsoft/TypeScript/tsc/internal/tspath"
)

// PluginName is the compilerOptions.plugins entry name that activates the
// plugin ("Avec des si on mettrait Paris en bouteille").
const PluginName = "@conmoong/paris"

// ValueKind classifies a configured variable's value. Values sourced from
// the environment, a .env file, or a plain file are strings; tsconfig
// literals and JSON files carry their own types.
type ValueKind int

const (
	KindString ValueKind = iota
	KindNumber
	KindBool
	KindJSON  // any other JSON value: object, array, or null
	KindBytes // {from: "file"} with type uint8array or buffer
)

// Value is one configured variable's build-time value.
type Value struct {
	Kind   ValueKind
	Str    string
	Num    float64
	Bool   bool
	JSON   any    // for KindJSON
	Bytes  []byte // for KindBytes
	Buffer bool   // KindBytes: emit Buffer.from(...) instead of new Uint8Array(...)
}

// Options is the plugin's resolved configuration. Configuration problems do
// not stop options from being constructed — they are carried in ConfigErrors
// and reported as build-failing diagnostics, per the plugin's
// explicit-and-loud design.
type Options struct {
	Values       map[string]Value
	ConfigErrors []string
}

// OptionsFromConfig reads the plugin's tsconfig entry from the raw parsed
// config. configDir anchors relative file/.env paths (the tsconfig's
// directory). Returns nil when the plugin is not configured.
func OptionsFromConfig(raw any, configDir string) *Options {
	entry, ok := hooks.FindPluginEntry(raw, PluginName)
	if !ok {
		return nil
	}
	options := &Options{Values: make(map[string]Value)}
	defineRaw, ok := hooks.ConfigGet(entry, "define")
	if !ok {
		return options
	}

	// Collect entries first so precedence is deterministic regardless of
	// map iteration order: "*" imports, then prefix imports, then exact
	// names — later stages overwrite earlier ones.
	type definition struct {
		name string
		spec any
	}
	var star, prefixes, exacts []definition
	hooks.ConfigEach(defineRaw, func(name string, spec any) {
		switch {
		case name == "*":
			star = append(star, definition{name, spec})
		case strings.HasSuffix(name, "*"):
			prefixes = append(prefixes, definition{name, spec})
		default:
			exacts = append(exacts, definition{name, spec})
		}
	})
	byName := func(definitions []definition) {
		sort.Slice(definitions, func(i, j int) bool { return definitions[i].name < definitions[j].name })
	}
	byName(star)
	byName(prefixes)
	byName(exacts)

	for _, definition := range append(star, prefixes...) {
		options.resolveWildcard(definition.name, definition.spec, configDir)
	}
	for _, definition := range exacts {
		options.resolveExact(definition.name, definition.spec, configDir)
	}
	return options
}

func (o *Options) addError(format string, args ...any) {
	o.ConfigErrors = append(o.ConfigErrors, fmt.Sprintf(format, args...))
}

// classify converts a parsed-JSON literal into a Value.
func classify(v any) Value {
	switch value := v.(type) {
	case string:
		return Value{Kind: KindString, Str: value}
	case float64:
		return Value{Kind: KindNumber, Num: value}
	case bool:
		return Value{Kind: KindBool, Bool: value}
	default:
		return Value{Kind: KindJSON, JSON: v}
	}
}

func (o *Options) resolveExact(name string, spec any, configDir string) {
	// Non-object shorthand: the literal itself is the value.
	switch spec.(type) {
	case string, float64, bool:
		o.Values[name] = classify(spec)
		return
	}
	if literal, ok := hooks.ConfigGet(spec, "value"); ok {
		o.Values[name] = classify(literal)
		return
	}
	from, ok := hooks.ConfigGet(spec, "from")
	if !ok {
		o.addError(`define %q: an object entry needs "value" or "from" (wrap literal objects in {"value": ...})`, name)
		return
	}
	switch from {
	case "env":
		envName := name
		if override, ok := hooks.ConfigGet(spec, "name"); ok {
			if s, ok := override.(string); ok && s != "" {
				envName = s
			} else {
				o.addError(`define %q: "name" must be a non-empty string`, name)
				return
			}
		}
		// A missing environment variable is not a configuration error: the
		// variable is simply undefined (ifDef exists for exactly that).
		if value, ok := os.LookupEnv(envName); ok {
			o.Values[name] = Value{Kind: KindString, Str: value}
		}
	case "file":
		o.resolveFile(name, spec, configDir)
	case "dot_env":
		entries, ok := o.loadDotEnv(name, spec, configDir)
		if !ok {
			return
		}
		key := name
		if override, ok := hooks.ConfigGet(spec, "name"); ok {
			if s, ok := override.(string); ok && s != "" {
				key = s
			} else {
				o.addError(`define %q: "name" must be a non-empty string`, name)
				return
			}
		}
		if value, ok := entries[key]; ok {
			o.Values[name] = Value{Kind: KindString, Str: value}
		}
	default:
		o.addError(`define %q: unknown source %q (expected "env", "file", or "dot_env")`, name, from)
	}
}

func (o *Options) resolveFile(name string, spec any, configDir string) {
	pathValue, ok := hooks.ConfigGet(spec, "path")
	path, isString := pathValue.(string)
	if !ok || !isString || path == "" {
		o.addError(`define %q: {from: "file"} needs a non-empty "path"`, name)
		return
	}
	fileType := "string"
	if typeValue, ok := hooks.ConfigGet(spec, "type"); ok {
		if s, ok := typeValue.(string); ok {
			fileType = s
		} else {
			o.addError(`define %q: "type" must be a string`, name)
			return
		}
	}
	content, err := os.ReadFile(resolvePath(configDir, path))
	if err != nil {
		o.addError("define %q: cannot read file: %v", name, err)
		return
	}
	switch fileType {
	case "string":
		o.Values[name] = Value{Kind: KindString, Str: string(content)}
	case "uint8array":
		o.Values[name] = Value{Kind: KindBytes, Bytes: content}
	case "buffer":
		o.Values[name] = Value{Kind: KindBytes, Bytes: content, Buffer: true}
	case "json":
		var parsed any
		if err := json.Unmarshal(content, &parsed); err != nil {
			o.addError("define %q: file is not valid JSON: %v", name, err)
			return
		}
		o.Values[name] = classify(parsed)
	default:
		o.addError(`define %q: unknown file type %q (expected "string", "uint8array", "buffer", or "json")`, name, fileType)
	}
}

func (o *Options) resolveWildcard(pattern string, spec any, configDir string) {
	from, ok := hooks.ConfigGet(spec, "from")
	if !ok {
		o.addError(`define %q: wildcard entries need {"from": "env"} or {"from": "dot_env"}`, pattern)
		return
	}
	var entries map[string]string
	switch from {
	case "env":
		entries = make(map[string]string)
		for _, pair := range os.Environ() {
			if key, value, found := strings.Cut(pair, "="); found {
				entries[key] = value
			}
		}
	case "dot_env":
		var ok bool
		entries, ok = o.loadDotEnv(pattern, spec, configDir)
		if !ok {
			return
		}
	default:
		o.addError(`define %q: wildcard entries support only "env" and "dot_env" sources, not %q`, pattern, from)
		return
	}
	prefix := strings.TrimSuffix(pattern, "*")
	for key, value := range entries {
		if strings.HasPrefix(key, prefix) {
			o.Values[key] = Value{Kind: KindString, Str: value}
		}
	}
}

func (o *Options) loadDotEnv(name string, spec any, configDir string) (map[string]string, bool) {
	path := ".env"
	if pathValue, ok := hooks.ConfigGet(spec, "path"); ok {
		s, isString := pathValue.(string)
		if !isString {
			o.addError(`define %q: "path" must be a string`, name)
			return nil, false
		}
		if s != "" {
			path = s
		}
	}
	content, err := os.ReadFile(resolvePath(configDir, path))
	if err != nil {
		o.addError("define %q: cannot read .env file: %v", name, err)
		return nil, false
	}
	entries, err := parseDotEnv(string(content))
	if err != nil {
		o.addError("define %q: invalid .env file %q: %v", name, path, err)
		return nil, false
	}
	return entries, true
}

// parseDotEnv parses the common .env subset: KEY=VALUE lines, blank lines,
// # comments, an optional "export " prefix, and optional single or double
// quotes around the value. No multi-line values or escape processing.
func parseDotEnv(content string) (map[string]string, error) {
	entries := make(map[string]string)
	for i, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		key, value, found := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		if !found || key == "" {
			return nil, fmt.Errorf("line %d is not KEY=VALUE", i+1)
		}
		value = strings.TrimSpace(value)
		if len(value) >= 2 {
			if (value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'') {
				value = value[1 : len(value)-1]
			}
		}
		entries[key] = value
	}
	return entries, nil
}

func resolvePath(configDir string, path string) string {
	if tspath.IsRootedDiskPath(path) {
		return path
	}
	return tspath.CombinePaths(configDir, path)
}
