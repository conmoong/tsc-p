package pathrewrite

import (
	"github.com/microsoft/TypeScript/tsc/internal/core"
	"github.com/microsoft/TypeScript/tsc/internal/tscp/hooks"
)

// PluginName is the name a tsconfig plugins entry uses to enable and
// configure this plugin:
//
//	{
//	    "compilerOptions": {
//	        "plugins": [
//	            {
//	                "name": "@conmoong/path-rewrite",
//	                "extension": true,
//	                "declarations": true,
//	                "alias": {
//	                    "@app/*": true,
//	                    "@keep/*": false,
//	                    "@web/*": { "enabled": true, "extension": false },
//	                    "./*": true
//	                }
//	            }
//	        ]
//	    }
//	}
//
// Alias patterns match the written specifier of every import — aliased or
// relative alike, so "./*" and "../*" address relative imports.
//
// The compilerOptions.plugins array is upstream's language-service plugin
// list; entries with other names are ignored here, and this entry is
// ignored by the language service. Nothing is ever loaded dynamically: the
// name only selects this compiled-in plugin.
const PluginName = "@conmoong/path-rewrite"

// Options configures the host rewriter. Every rewrite produces a relative
// path to the resolved file; the extension settings decide whether that
// path carries the destination-derived extension or is extensionless
// (.json always keeps its extension either way).
type Options struct {
	// Extension is the global default for whether rewritten specifiers
	// carry the destination-derived extension. Default true.
	Extension bool
	// Declarations controls whether the plugin also runs in the
	// declaration (.d.ts) pipeline. Default true.
	Declarations bool

	// aliasEnabled is the blanket setting applied to any specifier no
	// alias rule matches (and, with no rules at all, to every specifier).
	// Default true.
	aliasEnabled bool
	// aliasRules are per-pattern overrides matched against the written
	// specifier — aliased or relative alike ("./*" and "../*" address
	// relative imports) — with the same single-* pattern semantics as
	// tsconfig paths keys: an exact match beats any star match, otherwise
	// the longest matched prefix wins. Unmatched specifiers fall back to
	// aliasEnabled and the global extension setting.
	aliasRules []aliasRule
}

type aliasRule struct {
	pattern core.Pattern
	enabled bool
	// extension overrides the global extension setting for this pattern;
	// nil inherits it.
	extension *bool
}

func defaultOptions() *Options {
	return &Options{Extension: true, Declarations: true, aliasEnabled: true}
}

// decide reports whether a specifier should be rewritten and, if so,
// whether the rewritten path should carry the destination-derived
// extension.
func (o *Options) decide(specifier string) (rewrite bool, withExtension bool) {
	enabled := o.aliasEnabled
	extension := o.Extension
	best := -2 // exact matches use -1; start below it
	for _, rule := range o.aliasRules {
		if !rule.pattern.Matches(specifier) {
			continue
		}
		if rule.pattern.StarIndex == -1 {
			// Exact match always wins.
			return rule.enabled, ruleExtension(rule, o.Extension)
		}
		if rule.pattern.StarIndex > best {
			best = rule.pattern.StarIndex
			enabled = rule.enabled
			extension = ruleExtension(rule, o.Extension)
		}
	}
	return enabled, extension
}

func ruleExtension(rule aliasRule, globalExtension bool) bool {
	if rule.extension != nil {
		return *rule.extension
	}
	return globalExtension
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
	if value, ok := hooks.ConfigGet(entry, "extension"); ok {
		if b, ok := value.(bool); ok {
			options.Extension = b
		}
	}
	if value, ok := hooks.ConfigGet(entry, "declarations"); ok {
		if b, ok := value.(bool); ok {
			options.Declarations = b
		}
	}
	if value, ok := hooks.ConfigGet(entry, "alias"); ok {
		switch alias := value.(type) {
		case bool:
			options.aliasEnabled = alias
		default:
			hooks.ConfigEach(value, func(pattern string, ruleValue any) {
				if rule, ok := parseAliasRule(pattern, ruleValue); ok {
					options.aliasRules = append(options.aliasRules, rule)
				}
			})
		}
	}
	return options
}

func parseAliasRule(pattern string, value any) (aliasRule, bool) {
	parsed := core.TryParsePattern(pattern)
	if !parsed.IsValid() {
		return aliasRule{}, false
	}
	rule := aliasRule{pattern: parsed, enabled: true}
	switch v := value.(type) {
	case bool:
		rule.enabled = v
	default:
		if enabledValue, ok := hooks.ConfigGet(value, "enabled"); ok {
			if b, ok := enabledValue.(bool); ok {
				rule.enabled = b
			}
		}
		if extensionValue, ok := hooks.ConfigGet(value, "extension"); ok {
			if b, ok := extensionValue.(bool); ok {
				rule.extension = &b
			}
		}
	}
	return rule, true
}
