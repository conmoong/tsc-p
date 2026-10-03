// Package defaults decides which tsc-p emit plugins are active in a normal
// tsc-p compilation, where the compiler host is upstream's own and does not
// implement hooks.Provider. It exists as a separate package (rather than
// living in hooks) because it imports concrete plugin packages, which in
// turn import hooks.
//
// Activation is opt-in through the project's tsconfig: each plugin reads
// its own entry in compilerOptions.plugins (matched by name, ts-patch
// style). With no entry configured, emit output stays byte-identical to
// the pinned upstream compiler. Nothing is ever loaded dynamically — the
// plugins array only selects and configures compiled-in plugins.
package defaults

import (
	"sync"

	"github.com/microsoft/TypeScript/tsc/internal/tscp/bouncer"
	"github.com/microsoft/TypeScript/tsc/internal/tscp/graph"
	"github.com/microsoft/TypeScript/tsc/internal/tscp/hooks"
	"github.com/microsoft/TypeScript/tsc/internal/tscp/paris"
	"github.com/microsoft/TypeScript/tsc/internal/tscp/pathrewrite"
	"github.com/microsoft/TypeScript/tsc/internal/tscp/pure"
)

// EmitPlugins returns the emit plugins enabled by the raw parsed tsconfig
// (tsoptions.ParsedCommandLine.Raw), or nil when none are configured, in
// which case emit behaves exactly as upstream. configDir is the tsconfig's
// directory, anchoring plugin-configured relative paths; configPath is the
// tsconfig's own file path (used by @conmoong/graph to name its default
// output file after the tsconfig's basename), "" when there is no config
// file at all.
//
// The result is cached per rawConfig identity when the raw value is
// comparable (the real parser produces a *collections.OrderedMap): the
// emitter asks for plugins several times per file, plugins like paris and
// graph do file I/O at construction/across files, and stateful plugins
// must be shared across a compilation's files anyway for their
// diagnostics (and, for graph, its accumulated facts) to be correct.
func EmitPlugins(rawConfig any, configDir string, configPath string) []hooks.EmitPlugin {
	type cacheKey struct {
		raw        any
		configDir  string
		configPath string
	}
	if comparable_ := isComparable(rawConfig); comparable_ {
		key := cacheKey{rawConfig, configDir, configPath}
		if cached, ok := pluginCache.Load(key); ok {
			return cached.([]hooks.EmitPlugin)
		}
		plugins := buildPlugins(rawConfig, configDir, configPath)
		actual, _ := pluginCache.LoadOrStore(key, plugins)
		return actual.([]hooks.EmitPlugin)
	}
	return buildPlugins(rawConfig, configDir, configPath)
}

var pluginCache sync.Map

func isComparable(value any) bool {
	switch value.(type) {
	case nil, map[string]any:
		return false
	default:
		return true
	}
}

func buildPlugins(rawConfig any, configDir string, configPath string) []hooks.EmitPlugin {
	var plugins []hooks.EmitPlugin
	if options := pathrewrite.OptionsFromConfig(rawConfig); options != nil {
		plugins = append(plugins, pathrewrite.NewHostPlugin(options))
	}
	if options := bouncer.OptionsFromConfig(rawConfig); options != nil {
		plugins = append(plugins, bouncer.NewPlugin(options))
	}
	if options := paris.OptionsFromConfig(rawConfig, configDir); options != nil {
		plugins = append(plugins, paris.NewPlugin(options))
	}
	if options := pure.OptionsFromConfig(rawConfig); options != nil {
		plugins = append(plugins, pure.NewPlugin(options))
	}
	if options := graph.OptionsFromConfig(rawConfig, configDir, configPath); options != nil {
		plugins = append(plugins, graph.NewPlugin(options))
	}
	return plugins
}
