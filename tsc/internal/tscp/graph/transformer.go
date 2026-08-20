package graph

import (
	"path/filepath"
	"strings"

	"github.com/microsoft/TypeScript/tsc/internal/ast"
	"github.com/microsoft/TypeScript/tsc/internal/printer"
	"github.com/microsoft/TypeScript/tsc/internal/transformers"
	"github.com/microsoft/TypeScript/tsc/internal/tscp/pathrewrite"
)

// Transformer walks a file's script AST purely to gather facts (files,
// exports, edges) into the plugin's shared accumulator — it never
// modifies a node; every visit returns its input unchanged, so this
// plugin has zero effect on emitted output regardless of configuration
// (byte-parity holds even when the plugin is active, unlike every other
// tsc-p plugin). Specifier resolution reuses pathrewrite.ResolveSpecifier,
// the exact tiered mechanism pathrewrite's own rewriting relies on.
//
// Registered LAST among script transformers (see defaults.go), so the
// facts gathered reflect the FINAL, post-transform module surface: a
// bouncer-removed export never appears; a paris-pruned branch's imports
// are never visited at all, since the branch itself is already gone by
// the time this transformer runs.
type Transformer struct {
	transformers.Transformer
	acc       *accumulator
	host      printer.EmitHost
	configDir string
	file      *ast.SourceFile
	relPath   string
}

// NewTransformer creates the fact-gathering transformer for one file's
// script emit.
func NewTransformer(emitContext *printer.EmitContext, host printer.EmitHost, acc *accumulator, configDir string) *transformers.Transformer {
	tx := &Transformer{acc: acc, host: host, configDir: configDir}
	return tx.NewTransformer(tx.visit, emitContext)
}

func (tx *Transformer) visit(node *ast.Node) *ast.Node {
	switch node.Kind {
	case ast.KindSourceFile:
		return tx.visitSourceFile(node)
	case ast.KindImportDeclaration:
		tx.visitImportDeclaration(node)
		return node
	case ast.KindExportDeclaration:
		tx.visitExportDeclaration(node)
		return node
	case ast.KindImportEqualsDeclaration:
		tx.visitImportEqualsDeclaration(node)
		return node
	case ast.KindExportAssignment:
		tx.visitExportAssignment(node)
		return tx.Visitor().VisitEachChild(node)
	case ast.KindFunctionDeclaration, ast.KindClassDeclaration:
		tx.visitExportableDeclaration(node)
		return tx.Visitor().VisitEachChild(node)
	case ast.KindVariableStatement:
		tx.visitVariableStatement(node)
		return tx.Visitor().VisitEachChild(node)
	case ast.KindCallExpression:
		tx.visitCallExpression(node)
		return tx.Visitor().VisitEachChild(node)
	default:
		return tx.Visitor().VisitEachChild(node)
	}
}

func (tx *Transformer) visitSourceFile(node *ast.Node) *ast.Node {
	file := node.AsSourceFile()
	tx.file = file
	tx.relPath = tx.relativePath(file.FileName())
	tx.acc.addFile(tx.relPath, file)
	return tx.Visitor().VisitEachChild(node)
}

// relativePath returns fileName relative to configDir, always "./"- or
// "../"-prefixed (e.g. "./src/index.ts", never bare "src/index.ts") so
// rule patterns can consistently use the same "./"-prefixed convention
// pathrewrite's own alias patterns already use for relative specifiers
// (see the design's example configs, e.g. "./*", "./src/*").
func (tx *Transformer) relativePath(fileName string) string {
	rel, err := filepath.Rel(tx.configDir, fileName)
	if err != nil {
		return fileName
	}
	rel = filepath.ToSlash(rel)
	if !strings.HasPrefix(rel, "./") && !strings.HasPrefix(rel, "../") {
		rel = "./" + rel
	}
	return rel
}

// visitImportDeclaration handles `import ... from "x"` (including bare
// side-effect imports).
func (tx *Transformer) visitImportDeclaration(node *ast.Node) {
	declaration := node.AsImportDeclaration()
	if !ast.IsStringLiteralLike(declaration.ModuleSpecifier) {
		return
	}
	specifierText := declaration.ModuleSpecifier.Text()
	names, isDefault, isNamespace := importedNames(declaration.ImportClause)
	tx.addEdge(declaration.ModuleSpecifier, specifierText, names, isDefault, isNamespace)
}

// visitExportDeclaration handles `export { a, b } [from "x"]`,
// `export * [as ns] from "x"`. Named re-exports from a specifier also
// contribute export facts for this file (the names are re-exported from
// here, whether or not "x" resolves).
func (tx *Transformer) visitExportDeclaration(node *ast.Node) {
	declaration := node.AsExportDeclaration()
	var names []string
	isNamespace := false
	if declaration.ExportClause != nil {
		switch declaration.ExportClause.Kind {
		case ast.KindNamedExports:
			for _, element := range declaration.ExportClause.AsNamedExports().Elements.Nodes {
				specifier := element.AsExportSpecifier()
				exported := specifier.Name().Text()
				names = append(names, exported)
				tx.acc.addExport(tx.relPath, exported)
			}
		case ast.KindNamespaceExport:
			isNamespace = true
			tx.acc.addExport(tx.relPath, declaration.ExportClause.AsNamespaceExport().Name().Text())
		}
	}
	if declaration.ModuleSpecifier != nil && ast.IsStringLiteralLike(declaration.ModuleSpecifier) {
		tx.addEdge(declaration.ModuleSpecifier, declaration.ModuleSpecifier.Text(), names, false, isNamespace)
	}
}

// visitImportEqualsDeclaration handles `import x = require("y")` — a
// whole-module binding; no name list is attempted (mirrors the ESM
// namespace-import case).
func (tx *Transformer) visitImportEqualsDeclaration(node *ast.Node) {
	if !ast.IsExternalModuleImportEqualsDeclaration(node) {
		return
	}
	ref := node.AsImportEqualsDeclaration().ModuleReference.AsExternalModuleReference()
	if !ast.IsStringLiteralLike(ref.Expression) {
		return
	}
	tx.addEdge(ref.Expression, ref.Expression.Text(), nil, false, true)
	if ast.HasSyntacticModifier(node, ast.ModifierFlagsExport) {
		tx.acc.addExport(tx.relPath, node.AsImportEqualsDeclaration().Name().Text())
	}
}

// visitExportAssignment handles `export default expr;`
// (IsExportEquals == false). `export = expr;` (IsExportEquals == true, a
// CommonJS-style single export) is not modelled as a named export in v1 —
// a known, documented simplification.
func (tx *Transformer) visitExportAssignment(node *ast.Node) {
	assignment := node.AsExportAssignment()
	if !assignment.IsExportEquals {
		tx.acc.addExport(tx.relPath, "default")
	}
}

// visitExportableDeclaration handles `export [default] function/class`.
func (tx *Transformer) visitExportableDeclaration(node *ast.Node) {
	if !ast.HasSyntacticModifier(node, ast.ModifierFlagsExport) {
		return
	}
	if ast.HasSyntacticModifier(node, ast.ModifierFlagsDefault) {
		tx.acc.addExport(tx.relPath, "default")
		return
	}
	var name *ast.Node
	switch node.Kind {
	case ast.KindFunctionDeclaration:
		name = node.AsFunctionDeclaration().Name()
	case ast.KindClassDeclaration:
		name = node.AsClassDeclaration().Name()
	}
	if name != nil && name.Kind == ast.KindIdentifier {
		tx.acc.addExport(tx.relPath, name.Text())
	}
}

// visitVariableStatement handles `export const a = 1, b = 2;`. Only
// simple identifier bindings are recorded; a destructuring binding
// (`export const { a, b } = obj;`) is a known, documented v1 gap.
func (tx *Transformer) visitVariableStatement(node *ast.Node) {
	if !ast.HasSyntacticModifier(node, ast.ModifierFlagsExport) {
		return
	}
	for _, declarator := range node.AsVariableStatement().DeclarationList.AsVariableDeclarationList().Declarations.Nodes {
		name := declarator.AsVariableDeclaration().Name()
		if name != nil && name.Kind == ast.KindIdentifier {
			tx.acc.addExport(tx.relPath, name.Text())
		}
	}
}

// visitCallExpression handles `require("x")` and `import("x")`, which
// (unlike the declaration forms above) can appear anywhere in a file, not
// only at the top level.
func (tx *Transformer) visitCallExpression(node *ast.Node) {
	call := node.AsCallExpression()
	switch {
	case ast.IsRequireCall(node, true /*requireStringLiteralLikeArgument*/):
		tx.addEdge(call.Arguments.Nodes[0], call.Arguments.Nodes[0].Text(), nil, false, true)
	case ast.IsImportCall(node) && len(call.Arguments.Nodes) > 0 && ast.IsStringLiteralLike(call.Arguments.Nodes[0]):
		tx.addEdge(call.Arguments.Nodes[0], call.Arguments.Nodes[0].Text(), nil, false, true)
	}
}

// addEdge resolves specifierNode and records the resulting internal or
// external edge.
func (tx *Transformer) addEdge(specifierNode *ast.Node, specifierText string, names []string, isDefault bool, isNamespace bool) {
	edge := EdgeFact{
		From:        tx.relPath,
		Specifier:   specifierText,
		Names:       names,
		IsDefault:   isDefault,
		IsNamespace: isNamespace,
	}
	resolved := pathrewrite.ResolveSpecifier(tx.host, tx.file, specifierNode, specifierText)
	if resolved.IsResolved() {
		if resolved.IsExternalLibraryImport || strings.Contains(resolved.ResolvedFileName, "/node_modules/") {
			edge.Kind = KindExternal
			edge.ResolvedPackage = resolved.PackageId.Name
			if edge.ResolvedPackage == "" {
				// A relative resolution the resolver still flagged
				// external (rare, but possible for some redirected
				// resolutions): fall back to the written specifier so the
				// edge is never silently dropped of identifying info.
				edge.ResolvedPackage = specifierText
			}
		} else {
			edge.Kind = KindInternal
			edge.To = tx.relativePath(resolved.ResolvedFileName)
		}
	} else {
		// Unresolved: still recorded, as an external edge with no
		// resolved package identity — a bare specifier the compiler
		// itself could not find is exactly the kind of fact worth
		// surfacing, not silently dropping.
		edge.Kind = KindExternal
	}
	tx.acc.addEdge(edge)
}

// importedNames extracts the named bindings, default-import flag, and
// namespace-import flag from an import clause (nil for a bare
// `import "x"` side-effect import).
func importedNames(clause *ast.Node) (names []string, isDefault bool, isNamespace bool) {
	if clause == nil {
		return nil, false, false
	}
	importClause := clause.AsImportClause()
	if importClause.Name() != nil {
		isDefault = true
	}
	if importClause.NamedBindings != nil {
		switch importClause.NamedBindings.Kind {
		case ast.KindNamespaceImport:
			isNamespace = true
		case ast.KindNamedImports:
			for _, element := range importClause.NamedBindings.AsNamedImports().Elements.Nodes {
				specifier := element.AsImportSpecifier()
				exported := specifier.Name().Text()
				if specifier.PropertyName != nil {
					exported = specifier.PropertyName.Text()
				}
				names = append(names, exported)
			}
		}
	}
	return names, isDefault, isNamespace
}
