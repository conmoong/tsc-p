package graph

import (
	"encoding/json"
	"sort"
	"sync"

	"github.com/microsoft/TypeScript/tsc/internal/ast"
)

// Graph is the plugin's fact-only output: no rule configuration, no
// severities, no pass/fail judgements — just what the module graph looks
// like, so it can be checked by tsc-p itself (inline rules mode),
// aggregated across a workspace by graph-validate, or one day read by a
// bundler's tree-shaking pass. Paths are relative to ConfigDir (the
// tsconfig's own directory).
type Graph struct {
	Package  string       `json:"package,omitempty"`
	Tsconfig string       `json:"tsconfig,omitempty"`
	Files    []string     `json:"files"`
	Exports  []ExportFact `json:"exports"`
	Edges    []EdgeFact   `json:"edges"`
}

// ExportFact is one named (or default) export of a file.
type ExportFact struct {
	File string `json:"file"`
	Name string `json:"name"`
}

// EdgeFact is one module reference: an import, export-from, require(), or
// dynamic import() in From, reaching either another project file (To) or
// an external/unresolved specifier (Specifier, with ResolvedPackage set
// when the specifier resolved into node_modules). Names lists the
// specific named bindings the source syntax reaches for — useful, per
// design, as a future bundler tree-shaking input, not just for tsc-p's
// own checks — independent of IsDefault/IsNamespace, which record a
// default import and a namespace ("import * as ns") import respectively
// (a namespace import's actual property usage is not tracked; that would
// need deeper analysis than this fact-gathering pass performs).
type EdgeFact struct {
	From            string   `json:"from"`
	To              string   `json:"to,omitempty"`
	Specifier       string   `json:"specifier,omitempty"`
	Kind            string   `json:"kind"` // "internal" | "external"
	ResolvedPackage string   `json:"resolvedPackage,omitempty"`
	Names           []string `json:"names,omitempty"`
	IsDefault       bool     `json:"isDefault,omitempty"`
	IsNamespace     bool     `json:"isNamespace,omitempty"`
}

const (
	KindInternal = "internal"
	KindExternal = "external"
)

// accumulator collects facts across every file of one compilation,
// concurrently (tsc-p emits files in parallel) — every method is
// mutex-protected. A single accumulator is shared by one Plugin instance
// across the whole compilation.
type accumulator struct {
	mu          sync.Mutex
	files       map[string]bool
	exports     []ExportFact
	edges       []EdgeFact
	sourceFiles map[string]*ast.SourceFile // relPath -> file, for inline-diagnostic anchoring only; never serialized
}

func newAccumulator() *accumulator {
	return &accumulator{files: make(map[string]bool), sourceFiles: make(map[string]*ast.SourceFile)}
}

func (a *accumulator) addFile(path string, file *ast.SourceFile) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.files[path] = true
	a.sourceFiles[path] = file
}

// sourceFileFor looks up the *ast.SourceFile recorded for a relative path,
// for anchoring an inline diagnostic; nil if the path is unknown.
func (a *accumulator) sourceFileFor(path string) *ast.SourceFile {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.sourceFiles[path]
}

func (a *accumulator) addExport(file string, name string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.exports = append(a.exports, ExportFact{File: file, Name: name})
}

func (a *accumulator) addEdge(edge EdgeFact) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.edges = append(a.edges, edge)
}

// snapshot builds a deterministic Graph from everything accumulated so
// far. Called after every file (see graph.go): concurrent file processing
// means no single call is guaranteed to see the final state, but the
// snapshot taken after the LAST file to complete always does, and every
// snapshot is a fully well-formed graph in its own right, so writing one
// after each file and simply letting later writes overwrite earlier ones
// converges to the correct final result without needing to detect
// completion explicitly.
func (a *accumulator) snapshot(packageName string, tsconfigPath string) *Graph {
	a.mu.Lock()
	defer a.mu.Unlock()

	files := make([]string, 0, len(a.files))
	for file := range a.files {
		files = append(files, file)
	}
	sort.Strings(files)

	exports := make([]ExportFact, len(a.exports))
	copy(exports, a.exports)
	sort.Slice(exports, func(i, j int) bool {
		if exports[i].File != exports[j].File {
			return exports[i].File < exports[j].File
		}
		return exports[i].Name < exports[j].Name
	})

	edges := make([]EdgeFact, len(a.edges))
	copy(edges, a.edges)
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].From != edges[j].From {
			return edges[i].From < edges[j].From
		}
		if edges[i].To != edges[j].To {
			return edges[i].To < edges[j].To
		}
		return edges[i].Specifier < edges[j].Specifier
	})

	return &Graph{
		Package:  packageName,
		Tsconfig: tsconfigPath,
		Files:    files,
		Exports:  exports,
		Edges:    edges,
	}
}

// MarshalJSON is deliberately spelled out (rather than relying on struct
// tags alone) so nil slices marshal as "[]", never "null" — a facts
// artifact meant for external consumption should never surprise a reader
// expecting an array.
func (g *Graph) MarshalJSON() ([]byte, error) {
	type alias Graph
	copied := alias(*g)
	if copied.Files == nil {
		copied.Files = []string{}
	}
	if copied.Exports == nil {
		copied.Exports = []ExportFact{}
	}
	if copied.Edges == nil {
		copied.Edges = []EdgeFact{}
	}
	return json.MarshalIndent(copied, "", "    ")
}
