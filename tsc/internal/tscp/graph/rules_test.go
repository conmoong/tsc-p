package graph

import (
	"testing"

	"github.com/microsoft/TypeScript/tsc/internal/core"
	"gotest.tools/v3/assert"
)

func rule(pattern string, mutate func(*Rule)) Rule {
	r := Rule{Pattern: core.TryParsePattern(pattern), PatternText: pattern}
	mutate(&r)
	return r
}

func severity(s Severity) *Severity { return &s }

func TestFindCyclesDirect(t *testing.T) {
	t.Parallel()
	g := &Graph{
		Files: []string{"a.ts", "b.ts", "c.ts"},
		Edges: []EdgeFact{
			{From: "a.ts", To: "b.ts", Kind: KindInternal},
			{From: "b.ts", To: "a.ts", Kind: KindInternal},
			{From: "c.ts", To: "a.ts", Kind: KindInternal}, // not part of any cycle
		},
	}
	cyclic := findCycles(g)
	assert.Assert(t, cyclic["a.ts"] && cyclic["b.ts"])
	assert.Assert(t, !cyclic["c.ts"])
}

func TestFindCyclesSelfEdge(t *testing.T) {
	t.Parallel()
	g := &Graph{
		Files: []string{"a.ts"},
		Edges: []EdgeFact{{From: "a.ts", To: "a.ts", Kind: KindInternal}},
	}
	assert.Assert(t, findCycles(g)["a.ts"])
}

func TestFindCyclesNoCycle(t *testing.T) {
	t.Parallel()
	g := &Graph{
		Files: []string{"a.ts", "b.ts"},
		Edges: []EdgeFact{{From: "a.ts", To: "b.ts", Kind: KindInternal}},
	}
	cyclic := findCycles(g)
	assert.Equal(t, 0, len(cyclic))
}

func TestBestSeverityMostSpecificWins(t *testing.T) {
	t.Parallel()
	rules := []Rule{
		rule("./*", func(r *Rule) { r.Cycle = severity(SeverityWarning) }),
		rule("./src/*", func(r *Rule) { r.Cycle = severity(SeverityError) }),
	}
	s, ok := bestSeverity(rules, "./src/index.ts", func(r Rule) *Severity { return r.Cycle })
	assert.Assert(t, ok)
	assert.Equal(t, SeverityError, s, "the longer/more specific prefix must win over the broader */wildcard rule")
}

func TestBestSeverityExactBeatsStar(t *testing.T) {
	t.Parallel()
	rules := []Rule{
		rule("./*", func(r *Rule) { r.Cycle = severity(SeverityError) }),
		rule("./exact.ts", func(r *Rule) { r.Cycle = severity(SeverityAllow) }),
	}
	s, ok := bestSeverity(rules, "./exact.ts", func(r Rule) *Severity { return r.Cycle })
	assert.Assert(t, ok)
	assert.Equal(t, SeverityAllow, s)
}

func TestBestSeverityNoMatchingRule(t *testing.T) {
	t.Parallel()
	rules := []Rule{rule("./src/*", func(r *Rule) { r.Cycle = severity(SeverityError) })}
	_, ok := bestSeverity(rules, "./other/index.ts", func(r Rule) *Severity { return r.Cycle })
	assert.Assert(t, !ok, "a file no rule's pattern covers must not be checked at all")
}

func TestBestImportRuleSeverity(t *testing.T) {
	t.Parallel()
	rules := []Rule{
		rule("./src/features/*", func(r *Rule) {
			r.ImportRules = []ImportRule{
				{Pattern: core.TryParsePattern("./src/other-features/*"), Severity: SeverityError},
			}
		}),
	}
	s, ok := bestImportRuleSeverity(rules, "./src/features/a.ts", "./src/other-features/b.ts")
	assert.Assert(t, ok)
	assert.Equal(t, SeverityError, s)

	// Same "from", but a target not covered by any inner rule: allowed by default.
	s, ok = bestImportRuleSeverity(rules, "./src/features/a.ts", "./src/shared/util.ts")
	assert.Assert(t, ok)
	assert.Equal(t, SeverityAllow, s)

	// A "from" no outer rule covers at all.
	_, ok = bestImportRuleSeverity(rules, "./src/other/x.ts", "./src/shared/util.ts")
	assert.Assert(t, !ok)
}

func TestEvaluatePhantomAndDevLeak(t *testing.T) {
	t.Parallel()
	g := &Graph{
		Files: []string{"index.ts"},
		Edges: []EdgeFact{
			{From: "index.ts", Kind: KindExternal, ResolvedPackage: "lodash"},
			{From: "index.ts", Kind: KindExternal, ResolvedPackage: "typescript"},
			{From: "index.ts", Kind: KindExternal, ResolvedPackage: "declared-dep"},
		},
	}
	rules := []Rule{
		rule("*", func(r *Rule) {
			r.PhantomImport = severity(SeverityError)
			r.DevLeak = severity(SeverityWarning)
		}),
	}
	dependencies := map[string]bool{"declared-dep": true}
	devDependencies := map[string]bool{"typescript": true}

	findings := Evaluate(g, rules, dependencies, devDependencies)
	var sawPhantomLodash, sawDevLeakTypescript, sawPhantomTypescript bool
	for _, f := range findings {
		if f.Check == "phantomImport" && f.Message != "" {
			if contains(f.Message, "lodash") {
				sawPhantomLodash = true
			}
			if contains(f.Message, "typescript") {
				sawPhantomTypescript = true
			}
		}
		if f.Check == "devLeak" && contains(f.Message, "typescript") {
			sawDevLeakTypescript = true
		}
	}
	assert.Assert(t, sawPhantomLodash, "lodash is undeclared and must be flagged phantomImport")
	assert.Assert(t, sawPhantomTypescript, "phantomImport means \"not in real dependencies\" regardless of devDependency status — a devDependency is not a real dependency either")
	assert.Assert(t, sawDevLeakTypescript, "typescript is a devDependency used at runtime, must ALSO be flagged devLeak — the two checks are complementary, not mutually exclusive")
	for _, f := range findings {
		assert.Assert(t, !contains(f.Message, "declared-dep"), "a real dependency must never be flagged")
	}
}

func TestEvaluateUnusedFile(t *testing.T) {
	t.Parallel()
	g := &Graph{
		Files: []string{"index.ts", "helpers.ts", "orphan.ts"},
		Edges: []EdgeFact{
			{From: "index.ts", To: "helpers.ts", Kind: KindInternal},
		},
	}
	rules := []Rule{rule("*", func(r *Rule) { r.Unused = severity(SeverityWarning) })}
	findings := Evaluate(g, rules, nil, nil)
	var sawOrphan, sawHelpers, sawIndex bool
	for _, f := range findings {
		if f.Check != "unused" {
			continue
		}
		switch f.File {
		case "orphan.ts":
			sawOrphan = true
		case "helpers.ts":
			sawHelpers = true
		case "index.ts":
			sawIndex = true
		}
	}
	assert.Assert(t, sawOrphan, "a file never imported by anything must be flagged unused")
	assert.Assert(t, !sawHelpers, "a file that is imported must not be flagged")
	assert.Assert(t, sawIndex, "index.ts is never imported by anything else in this graph either, and is flagged unless a rule allows it (e.g. an entry-point pattern)")
}

func TestEvaluateUnusedPackage(t *testing.T) {
	t.Parallel()
	g := &Graph{
		Files: []string{"index.ts"},
		Edges: []EdgeFact{
			{From: "index.ts", Kind: KindExternal, ResolvedPackage: "lodash"},
		},
	}
	rules := []Rule{rule("*", func(r *Rule) { r.Unused = severity(SeverityWarning) })}
	dependencies := map[string]bool{"lodash": true, "moment": true}
	devDependencies := map[string]bool{"eslint": true}

	findings := Evaluate(g, rules, dependencies, devDependencies)
	var sawMoment, sawLodash, sawEslint bool
	for _, f := range findings {
		if f.Check != "unused" {
			continue
		}
		if contains(f.Message, "\"moment\"") {
			sawMoment = true
		}
		if contains(f.Message, "\"lodash\"") {
			sawLodash = true
		}
		if contains(f.Message, "\"eslint\"") {
			sawEslint = true
		}
	}
	assert.Assert(t, sawMoment, "a declared dependency never imported anywhere must be flagged unused")
	assert.Assert(t, !sawLodash, "a declared dependency that is imported must not be flagged")
	assert.Assert(t, sawEslint, "a declared devDependency never imported anywhere must also be flagged unused")
}

func TestEvaluateIncrementalExcludesUnused(t *testing.T) {
	t.Parallel()
	g := &Graph{
		Files: []string{"orphan.ts"},
	}
	rules := []Rule{rule("*", func(r *Rule) { r.Unused = severity(SeverityError) })}
	findings := EvaluateIncremental(g, rules, nil, nil)
	for _, f := range findings {
		assert.Assert(t, f.Check != "unused", "unused must never be reported by tsc-p's own inline evaluation")
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (func() bool {
		for i := 0; i+len(needle) <= len(haystack); i++ {
			if haystack[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}
