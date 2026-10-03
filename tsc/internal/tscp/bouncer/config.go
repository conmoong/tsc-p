package bouncer

import "github.com/microsoft/TypeScript/tsc/internal/tscp/hooks"

// PluginName is the name a tsconfig plugins entry uses to enable and
// configure this plugin:
//
//	{
//	    "compilerOptions": {
//	        "plugins": [
//	            {
//	                "name": "@conmoong/bouncer",
//	                "release": "beta",
//	                "profiles": {
//	                    "prod-hide": "no-export",
//	                    "make-default": "export-default",
//	                    "rename-thing": { "export-as": "NEW_NAME" },
//	                    "test-only": "export",
//	                    "internal-only": { "release": "internal" }
//	                }
//	            }
//	        ]
//	    }
//	}
//
// "release" (one of "public", "beta", "alpha", "internal") selects this
// build's channel; a "@bouncer release LEVEL" tag then trims the
// declaration from .d.ts output whenever LEVEL is more experimental than
// the configured channel — public < beta < alpha < internal, each channel
// including everything more stable than it. Declaration trimming only:
// the JavaScript for a release-tagged declaration is never touched,
// matching API Extractor's .d.ts-rollup semantics. Omitting "release"
// (or an unrecognised value) disables trimming; the tag then has no
// effect, so builds without a configured channel are unaffected by
// release tags already present in source.
//
// The compilerOptions.plugins array is upstream's language-service plugin
// list; entries with other names are ignored here, and this entry is
// ignored by the language service. Nothing is ever loaded dynamically: the
// name only selects this compiled-in plugin.
const PluginName = "@conmoong/bouncer"

// ActionKind is the effect a @bouncer tag (directly, or via a resolved
// profile) applies to a declaration.
type ActionKind int

const (
	// ActionNone applies no effect: the declaration is left unchanged.
	// This is also the result for any input this plugin cannot make sense
	// of — unrecognised tag syntax, a profile name with no matching
	// tsconfig entry, or an export-default/export-as directive on a
	// declaration with no single unambiguous name (for example a
	// multi-binding "const a = 1, b = 2;") — so a typo or an
	// unsupported shape never breaks a build; it only means the tag has
	// no effect.
	ActionNone ActionKind = iota
	// ActionRemove elides the declaration entirely, from whichever
	// pipelines the plugin runs in.
	ActionRemove
	// ActionExport ensures the declaration has a plain export modifier.
	// A no-op on a declaration that is already an "export default".
	ActionExport
	// ActionExportDefault makes the declaration's name the module's
	// default export: any existing export/default modifier on the
	// declaration itself is removed, and a separate
	// "export default <name>;" statement is appended after it. Two
	// declarations in the same file both resolving to this action produce
	// two default-export statements — invalid JavaScript. This plugin does
	// not detect or guard against that; see the package doc comment.
	ActionExportDefault
	// ActionExportAs exports the declaration under a different external
	// name: any existing export/default modifier on the declaration
	// itself is removed, and a separate "export { name as NewName };"
	// statement is appended after it. The declaration's own name, and all
	// references to it within the file, are unchanged. If NewName
	// collides with another export in the file, that is invalid
	// JavaScript; this plugin does not detect or guard against that.
	ActionExportAs
	// ActionRelease marks the declaration with a release maturity level
	// ("@bouncer release beta"); Name carries the level. When the plugin
	// entry configures "release", declarations whose level is more
	// experimental than the configured channel are omitted from
	// declaration output only — the JavaScript is never touched, matching
	// API Extractor's dts-trimming semantics. With no "release"
	// configured, the tag is a no-op annotation.
	ActionRelease
	// ActionNoExport removes the export modifier in script output, but
	// keeps the declaration itself (unlike ActionRemove, its
	// implementation is retained and usable elsewhere in the file). In
	// declaration output it goes further and omits the declaration
	// entirely, since a non-exported declaration has no public type
	// surface to publish. A no-op on a declaration that is already an
	// "export default".
	ActionNoExport
)

// Action is the parsed result of a @bouncer directive or resolved
// profile.
type Action struct {
	Kind ActionKind
	// Name is the target export name for ActionExportAs; unused otherwise.
	Name string
}

// releaseRank orders the API maturity channels: everything in a channel
// includes everything more stable than it. "internal" is the most
// experimental and appears only in an "internal" build.
var releaseRank = map[string]int{
	"public":   0,
	"beta":     1,
	"alpha":    2,
	"internal": 3,
}

// Options configures the bouncer plugin.
type Options struct {
	// Profiles maps a profile name (referenced as "@bouncer profile NAME")
	// to the action it resolves to. A name not present here — including
	// when Profiles is nil — resolves to ActionNone.
	Profiles map[string]Action
	// ReleaseRank is the configured release channel's rank ("release" in
	// the plugin entry): declarations tagged with a higher rank are
	// trimmed from declaration output. -1 (the default, or an
	// unrecognised value) disables trimming; release tags then annotate
	// without effect.
	ReleaseRank int
}

func defaultOptions() *Options {
	return &Options{ReleaseRank: -1}
}

// OptionsFromConfig extracts this plugin's options from a raw parsed
// tsconfig (tsoptions.ParsedCommandLine.Raw). It returns nil when the
// configuration does not enable the plugin.
func OptionsFromConfig(raw any) *Options {
	entry, ok := hooks.FindPluginEntry(raw, PluginName)
	if !ok {
		return nil
	}
	options := defaultOptions()
	if value, ok := hooks.ConfigGet(entry, "release"); ok {
		if channel, ok := value.(string); ok {
			if rank, ok := releaseRank[channel]; ok {
				options.ReleaseRank = rank
			}
		}
	}
	if value, ok := hooks.ConfigGet(entry, "profiles"); ok {
		options.Profiles = map[string]Action{}
		hooks.ConfigEach(value, func(name string, actionValue any) {
			if action, ok := parseProfileAction(actionValue); ok {
				options.Profiles[name] = action
			}
		})
	}
	return options
}

// parseProfileAction parses one profiles.<name> entry: the strings
// "remove", "export", "no-export", "export-default", or an object
// { "export-as": "NewName" } or { "release": "beta" }. Anything else (a
// malformed entry) is reported as not-ok, and the caller leaves that
// profile undefined — resolving through it behaves exactly like an
// unknown profile name (ActionNone), per this plugin's safe-default
// policy.
func parseProfileAction(value any) (Action, bool) {
	if s, ok := value.(string); ok {
		switch s {
		case "remove":
			return Action{Kind: ActionRemove}, true
		case "export":
			return Action{Kind: ActionExport}, true
		case "no-export":
			return Action{Kind: ActionNoExport}, true
		case "export-default":
			return Action{Kind: ActionExportDefault}, true
		}
		return Action{}, false
	}
	if nameValue, ok := hooks.ConfigGet(value, "export-as"); ok {
		if name, ok := nameValue.(string); ok && name != "" {
			return Action{Kind: ActionExportAs, Name: name}, true
		}
	}
	if channelValue, ok := hooks.ConfigGet(value, "release"); ok {
		if channel, ok := channelValue.(string); ok {
			if _, ok := releaseRank[channel]; ok {
				return Action{Kind: ActionRelease, Name: channel}, true
			}
		}
	}
	return Action{}, false
}
