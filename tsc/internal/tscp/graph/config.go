package graph

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/microsoft/TypeScript/tsc/internal/core"
	"github.com/microsoft/TypeScript/tsc/internal/tscp/hooks"
)

// PluginName is the compilerOptions.plugins entry name that activates the
// plugin.
const PluginName = "@conmoong/graph"

// Severity is the reported level for a rule match, or "allow" for an
// explicit no-op (used to carve out exceptions, and as importRules'
// default).
type Severity int

const (
	SeverityAllow Severity = iota
	SeverityWarning
	SeverityError
)

func parseSeverity(value any) (Severity, bool) {
	s, ok := value.(string)
	if !ok {
		return 0, false
	}
	switch s {
	case "error":
		return SeverityError, true
	case "warning":
		return SeverityWarning, true
	case "allow":
		return SeverityAllow, true
	default:
		return 0, false
	}
}

// ImportRule is one entry of a boundary ("importRules") list: an edge from
// the enclosing Rule's pattern to a target matching Module (a file glob or
// a resolved package name — importRules may target external packages) is
// reported at Severity. Default "allow": nothing is denied unless
// explicitly listed.
type ImportRule struct {
	Pattern  core.Pattern
	Severity Severity
}

// Rule is one (possibly merged) entry of the plugin's "rules" array. Module
// patterns use the same single-* syntax as tsconfig "paths" (core.Pattern):
// for phantomImport and importRules' inner list, Module matches a RESOLVED
// PACKAGE NAME; for unused, Module matches EITHER a project-relative file
// path or a resolved package name (unused is evaluated once per candidate
// kind — see rules.go); everywhere else, Module matches a project-relative
// file path.
type Rule struct {
	Pattern       core.Pattern
	PatternText   string
	PhantomImport *Severity
	Cycle         *Severity
	DevLeak       *Severity
	Unused        *Severity
	ImportRules   []ImportRule
}

// Options configures the plugin.
type Options struct {
	// EmitEnabled is true when "emit" was truthy (bool true or a non-empty
	// string). EmitPath is the resolved absolute output path, meaningful
	// only when EmitEnabled.
	EmitEnabled bool
	EmitPath    string
	// Rules is always parsed regardless of EmitEnabled: with EmitEnabled,
	// tsc-p itself never evaluates them (graph-validate does, later,
	// reading this same tsconfig) and merely notes that deferral; without
	// EmitEnabled, tsc-p evaluates them inline as ordinary diagnostics.
	Rules []Rule

	// ConfigDir anchors relative paths recorded in the emitted graph.
	ConfigDir string
	// Dependencies/DevDependencies are the nearest package.json's declared
	// dependency names, used by the phantomImport and devLeak checks. Both nil
	// if no package.json was found.
	Dependencies    map[string]bool
	DevDependencies map[string]bool
	// PackageName is the nearest package.json's own "name" field, recorded
	// in the emitted graph so an aggregator can identify which package it
	// belongs to. "" if no package.json was found or it has no name.
	PackageName string
}

// OptionsFromConfig extracts this plugin's options from a raw parsed
// tsconfig (tsoptions.ParsedCommandLine.Raw). It returns nil when the
// configuration does not enable the plugin. configDir is the tsconfig's
// directory; configPath is the tsconfig's own file path (used to derive
// the default emit output filename), "" if there is no config file.
func OptionsFromConfig(raw any, configDir string, configPath string) *Options {
	entry, ok := hooks.FindPluginEntry(raw, PluginName)
	if !ok {
		return nil
	}
	options := &Options{ConfigDir: configDir}

	if value, ok := hooks.ConfigGet(entry, "emit"); ok {
		switch v := value.(type) {
		case bool:
			options.EmitEnabled = v
			if v {
				options.EmitPath = defaultEmitPath(configDir, configPath)
			}
		case string:
			if v != "" {
				options.EmitEnabled = true
				options.EmitPath = resolveOutputPath(configDir, v)
			}
		}
	}

	if value, ok := hooks.ConfigGet(entry, "rules"); ok {
		if list, ok := value.([]any); ok {
			options.Rules = parseRules(list)
		}
	}

	deps, devDeps, name := loadNearestPackageJson(configDir)
	options.Dependencies = deps
	options.DevDependencies = devDeps
	options.PackageName = name

	return options
}

// defaultEmitPath is "<tsconfig-basename-without-extension>.graph.json"
// next to the tsconfig itself. Falls back to "tsconfig.graph.json" in
// configDir if there is no real config file path (should not normally
// happen for an activated plugin, but keeps the option well-defined).
func defaultEmitPath(configDir string, configPath string) string {
	if configPath == "" {
		return filepath.Join(configDir, "tsconfig.graph.json")
	}
	base := filepath.Base(configPath)
	base = base[:len(base)-len(filepath.Ext(base))]
	return filepath.Join(filepath.Dir(configPath), base+".graph.json")
}

func resolveOutputPath(configDir string, path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(configDir, path)
}

// parseRules parses the "rules" array, merging entries that share the
// exact same "module" pattern text (in array order — a later entry's
// fields override an earlier one's on conflict, including replacing an
// earlier importRules list wholesale rather than appending to it).
func parseRules(list []any) []Rule {
	var order []string
	byPattern := make(map[string]*Rule)
	for _, entry := range list {
		moduleValue, ok := hooks.ConfigGet(entry, "module")
		if !ok {
			continue
		}
		patternText, ok := moduleValue.(string)
		if !ok {
			continue
		}
		parsed := core.TryParsePattern(patternText)
		if !parsed.IsValid() {
			continue
		}
		rule, exists := byPattern[patternText]
		if !exists {
			rule = &Rule{Pattern: parsed, PatternText: patternText}
			byPattern[patternText] = rule
			order = append(order, patternText)
		}
		if value, ok := hooks.ConfigGet(entry, "phantomImport"); ok {
			if severity, ok := parseSeverity(value); ok {
				rule.PhantomImport = &severity
			}
		}
		if value, ok := hooks.ConfigGet(entry, "cycle"); ok {
			if severity, ok := parseSeverity(value); ok {
				rule.Cycle = &severity
			}
		}
		if value, ok := hooks.ConfigGet(entry, "devLeak"); ok {
			if severity, ok := parseSeverity(value); ok {
				rule.DevLeak = &severity
			}
		}
		if value, ok := hooks.ConfigGet(entry, "unused"); ok {
			if severity, ok := parseSeverity(value); ok {
				rule.Unused = &severity
			}
		}
		if value, ok := hooks.ConfigGet(entry, "importRules"); ok {
			if innerList, ok := value.([]any); ok {
				rule.ImportRules = parseImportRules(innerList)
			}
		}
	}
	rules := make([]Rule, 0, len(order))
	for _, patternText := range order {
		rules = append(rules, *byPattern[patternText])
	}
	return rules
}

func parseImportRules(list []any) []ImportRule {
	var rules []ImportRule
	for _, entry := range list {
		moduleValue, ok := hooks.ConfigGet(entry, "module")
		if !ok {
			continue
		}
		patternText, ok := moduleValue.(string)
		if !ok {
			continue
		}
		parsed := core.TryParsePattern(patternText)
		if !parsed.IsValid() {
			continue
		}
		severity := SeverityAllow
		if value, ok := hooks.ConfigGet(entry, "severity"); ok {
			if s, ok := parseSeverity(value); ok {
				severity = s
			}
		}
		rules = append(rules, ImportRule{Pattern: parsed, Severity: severity})
	}
	return rules
}

// minimalPackageJson reads only the two dependency fields graph's checks
// need; a full parse (peerDependencies, exports maps, etc.) is unnecessary
// for this purpose.
type minimalPackageJson struct {
	Name            string            `json:"name"`
	Dependencies    map[string]string `json:"dependencies"`
	DevDependencies map[string]string `json:"devDependencies"`
}

// loadNearestPackageJson walks up from startDir looking for the nearest
// package.json, stopping at a node_modules boundary or the filesystem
// root. Returns (nil, nil, "") if none is found or it cannot be parsed —
// phantomImport/devLeak/unused checks then simply never match (no dependency lists to
// compare against), rather than erroring the whole compilation over a
// concern orthogonal to the check itself.
func loadNearestPackageJson(startDir string) (dependencies map[string]bool, devDependencies map[string]bool, name string) {
	dir := startDir
	for {
		path := filepath.Join(dir, "package.json")
		if content, err := os.ReadFile(path); err == nil {
			var parsed minimalPackageJson
			if json.Unmarshal(content, &parsed) == nil {
				return toSet(parsed.Dependencies), toSet(parsed.DevDependencies), parsed.Name
			}
			return nil, nil, ""
		}
		parent := filepath.Dir(dir)
		if parent == dir || filepath.Base(dir) == "node_modules" {
			return nil, nil, ""
		}
		dir = parent
	}
}

func toSet(names map[string]string) map[string]bool {
	if names == nil {
		return nil
	}
	set := make(map[string]bool, len(names))
	for name := range names {
		set[name] = true
	}
	return set
}
