package graph

import (
	"fmt"
	"sort"
)

// Finding is one rule violation, evaluated against a Graph — used both by
// tsc-p's own inline evaluation (converted to diagnostics in graph.go) and
// as the reference algorithm for graph-validate's TypeScript reimplementation
// of the same rules over an aggregated multi-project graph.
type Finding struct {
	Check    string
	File     string
	Severity Severity
	Message  string
}

// EvaluateIncremental runs every check EXCEPT unused: cycle, phantomImport,
// devLeak, and importRules are all monotonic — once a finding is true
// against a partial (still-growing) Graph, more files being added can
// only add further findings, never invalidate one already found, because
// each is either an existential claim over what has been seen so far
// (cycle: these specific files import each other in a loop) or purely
// local to one edge (phantomImport/devLeak/importRules never depend on
// any other file's data at all). This makes them safe for tsc-p's own
// inline evaluation, called after every file during one compilation,
// without needing to know which call is the last one.
//
// unused is the one check excluded here: "this file/package is never
// imported anywhere" is a universal claim over the WHOLE graph that can
// only be soundly confirmed once every file is known — evaluating it
// against a partial graph risks a false positive that a later file (not
// yet processed) would have disproven. This function is therefore only
// ever safe to call against a partial, still-growing Graph; callers that
// know their graph is already complete (tsc-p's own Plugin, once it has
// seen every file, and graph-validate always) should call Evaluate
// directly instead, which includes unused.
func EvaluateIncremental(g *Graph, rules []Rule, dependencies map[string]bool, devDependencies map[string]bool) []Finding {
	findings := Evaluate(g, rules, dependencies, devDependencies)
	kept := findings[:0]
	for _, finding := range findings {
		if finding.Check != "unused" {
			kept = append(kept, finding)
		}
	}
	return kept
}

// Evaluate runs every configured rule against a fully-built Graph, using
// dependencies/devDependencies (the nearest package.json's declared
// dependency name sets; both may be nil) for the phantomImport, devLeak,
// and the package half of the unused check. Findings with
// Severity == SeverityAllow are never returned — "allow" exists only to
// let a more specific pattern override a broader deny, not as a
// reportable outcome.
func Evaluate(g *Graph, rules []Rule, dependencies map[string]bool, devDependencies map[string]bool) []Finding {
	var findings []Finding
	cyclic := findCycles(g)

	for _, file := range g.Files {
		if cyclic[file] {
			if severity, ok := bestSeverity(rules, file, func(r Rule) *Severity { return r.Cycle }); ok && severity > SeverityAllow {
				findings = append(findings, Finding{Check: "cycle", File: file, Severity: severity, Message: fmt.Sprintf("%s participates in an import cycle", file)})
			}
		}
	}

	for _, edge := range g.Edges {
		if edge.Kind != KindExternal || edge.ResolvedPackage == "" {
			continue
		}
		if dependencies[edge.ResolvedPackage] {
			continue // declared as a real dependency: not phantomImport, not a dev leak.
		}
		if severity, ok := bestSeverity(rules, edge.ResolvedPackage, func(r Rule) *Severity { return r.PhantomImport }); ok && severity > SeverityAllow {
			findings = append(findings, Finding{Check: "phantomImport", File: edge.From, Severity: severity, Message: fmt.Sprintf("%s imports %q, which is not declared in package.json dependencies", edge.From, edge.ResolvedPackage)})
		}
		if devDependencies[edge.ResolvedPackage] {
			if severity, ok := bestSeverity(rules, edge.From, func(r Rule) *Severity { return r.DevLeak }); ok && severity > SeverityAllow {
				findings = append(findings, Finding{Check: "devLeak", File: edge.From, Severity: severity, Message: fmt.Sprintf("%s imports %q, which is only a devDependency", edge.From, edge.ResolvedPackage)})
			}
		}
	}

	usedFiles, usedPackages := usage(g)
	for _, file := range g.Files {
		if usedFiles[file] {
			continue
		}
		if severity, ok := bestSeverity(rules, file, func(r Rule) *Severity { return r.Unused }); ok && severity > SeverityAllow {
			findings = append(findings, Finding{Check: "unused", File: file, Severity: severity, Message: fmt.Sprintf("%s is never imported anywhere in this project's graph", file)})
		}
	}
	for name := range dependencies {
		if usedPackages[name] {
			continue
		}
		if severity, ok := bestSeverity(rules, name, func(r Rule) *Severity { return r.Unused }); ok && severity > SeverityAllow {
			findings = append(findings, Finding{Check: "unused", File: "", Severity: severity, Message: fmt.Sprintf("dependency %q is declared in package.json but never imported anywhere in this project's graph", name)})
		}
	}
	for name := range devDependencies {
		if usedPackages[name] || dependencies[name] {
			continue
		}
		if severity, ok := bestSeverity(rules, name, func(r Rule) *Severity { return r.Unused }); ok && severity > SeverityAllow {
			findings = append(findings, Finding{Check: "unused", File: "", Severity: severity, Message: fmt.Sprintf("devDependency %q is declared in package.json but never imported anywhere in this project's graph", name)})
		}
	}

	for _, edge := range g.Edges {
		target := edge.To
		if edge.Kind == KindExternal {
			if edge.ResolvedPackage == "" {
				continue // unresolved specifier: nothing to match a boundary rule against.
			}
			target = edge.ResolvedPackage
		}
		if severity, ok := bestImportRuleSeverity(rules, edge.From, target); ok && severity > SeverityAllow {
			findings = append(findings, Finding{Check: "importRules", File: edge.From, Severity: severity, Message: fmt.Sprintf("%s must not import %q", edge.From, target)})
		}
	}

	sort.Slice(findings, func(i, j int) bool {
		if findings[i].File != findings[j].File {
			return findings[i].File < findings[j].File
		}
		return findings[i].Check < findings[j].Check
	})
	return findings
}

// usage indexes, for the whole graph, which files are ever the target of
// some internal edge (imported/required/dynamically-imported by anything,
// regardless of which names were reached) and which resolved package
// names are ever the target of some external edge — the two universes
// the unused check tests candidates against.
func usage(g *Graph) (usedFiles map[string]bool, usedPackages map[string]bool) {
	usedFiles = make(map[string]bool)
	usedPackages = make(map[string]bool)
	for _, edge := range g.Edges {
		switch edge.Kind {
		case KindInternal:
			if edge.To != "" {
				usedFiles[edge.To] = true
			}
		case KindExternal:
			if edge.ResolvedPackage != "" {
				usedPackages[edge.ResolvedPackage] = true
			}
		}
	}
	return usedFiles, usedPackages
}

// bestSeverity finds the most-specific rule among rules that define the
// given field, matched against candidate (a file path or a resolved
// package name, depending on the field). ok is false when no rule's
// pattern defines that field for candidate at all — meaning the check was
// never configured for this candidate, not that it passed.
//
// Deliberately not core.FindBestPatternMatch: that helper returns a
// zero-value item when nothing matches (there is no matched value to
// return), and a zero-value core.Pattern has StarIndex 0 (Go's int zero
// value) with an empty Text — which is a valid-looking "star pattern" to
// Pattern.Matches, whose slicing panics on it (an empty Text sliced from
// index 1). Tracking "matched anything" explicitly, in the same loop that
// finds the best match — mirroring pathrewrite's own alias-matching
// loop — avoids ever calling Matches on a pattern that was never a real
// rule to begin with.
func bestSeverity(rules []Rule, candidate string, field func(Rule) *Severity) (Severity, bool) {
	var best *Rule
	bestStarIndex := -2 // exact matches use -1; start below it
	for i := range rules {
		severity := field(rules[i])
		if severity == nil || !rules[i].Pattern.Matches(candidate) {
			continue
		}
		if rules[i].Pattern.StarIndex == -1 {
			return *severity, true // exact match always wins outright
		}
		if rules[i].Pattern.StarIndex > bestStarIndex {
			bestStarIndex = rules[i].Pattern.StarIndex
			best = &rules[i]
		}
	}
	if best == nil {
		return 0, false
	}
	return *field(*best), true
}

// bestImportRuleSeverity finds the most-specific rule (by outer pattern)
// matching from that has a non-empty ImportRules list, then within that
// single rule's list finds the most-specific inner pattern matching to.
// Default SeverityAllow if no outer rule with import rules matches from,
// or none of its inner rules match to. See bestSeverity's doc comment for
// why this uses an explicit matching loop rather than
// core.FindBestPatternMatch.
func bestImportRuleSeverity(rules []Rule, from string, to string) (Severity, bool) {
	var best *Rule
	bestStarIndex := -2
	for i := range rules {
		if len(rules[i].ImportRules) == 0 || !rules[i].Pattern.Matches(from) {
			continue
		}
		if rules[i].Pattern.StarIndex == -1 {
			best = &rules[i]
			break
		}
		if rules[i].Pattern.StarIndex > bestStarIndex {
			bestStarIndex = rules[i].Pattern.StarIndex
			best = &rules[i]
		}
	}
	if best == nil {
		return SeverityAllow, false
	}

	var bestInner *ImportRule
	bestInnerStarIndex := -2
	for i := range best.ImportRules {
		if !best.ImportRules[i].Pattern.Matches(to) {
			continue
		}
		if best.ImportRules[i].Pattern.StarIndex == -1 {
			bestInner = &best.ImportRules[i]
			break
		}
		if best.ImportRules[i].Pattern.StarIndex > bestInnerStarIndex {
			bestInnerStarIndex = best.ImportRules[i].Pattern.StarIndex
			bestInner = &best.ImportRules[i]
		}
	}
	if bestInner == nil {
		return SeverityAllow, true // the boundary rule is active for `from`, "to" just isn't restricted.
	}
	return bestInner.Severity, true
}

// findCycles returns the set of files that participate in an import cycle
// (a strongly-connected component of size > 1 in the internal-edges-only
// graph, or a file with a direct self-edge), via Tarjan's algorithm.
func findCycles(g *Graph) map[string]bool {
	adjacency := make(map[string][]string)
	for _, edge := range g.Edges {
		if edge.Kind == KindInternal && edge.To != "" {
			adjacency[edge.From] = append(adjacency[edge.From], edge.To)
		}
	}

	var index int
	indices := make(map[string]int)
	lowlink := make(map[string]int)
	onStack := make(map[string]bool)
	var stack []string
	cyclic := make(map[string]bool)

	var strongConnect func(v string)
	strongConnect = func(v string) {
		indices[v] = index
		lowlink[v] = index
		index++
		stack = append(stack, v)
		onStack[v] = true

		for _, w := range adjacency[v] {
			if _, visited := indices[w]; !visited {
				strongConnect(w)
				if lowlink[w] < lowlink[v] {
					lowlink[v] = lowlink[w]
				}
			} else if onStack[w] {
				if indices[w] < lowlink[v] {
					lowlink[v] = indices[w]
				}
			}
		}

		if lowlink[v] == indices[v] {
			var component []string
			for {
				n := len(stack) - 1
				w := stack[n]
				stack = stack[:n]
				onStack[w] = false
				component = append(component, w)
				if w == v {
					break
				}
			}
			if len(component) > 1 {
				for _, w := range component {
					cyclic[w] = true
				}
			} else if len(component) == 1 {
				// A single-node component is only a cycle if it has a
				// direct self-edge (v -> v).
				for _, w := range adjacency[component[0]] {
					if w == component[0] {
						cyclic[component[0]] = true
						break
					}
				}
			}
		}
	}

	for _, file := range g.Files {
		if _, visited := indices[file]; !visited {
			strongConnect(file)
		}
	}
	return cyclic
}
