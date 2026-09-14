package bouncer

import (
	"github.com/microsoft/TypeScript/tsc/internal/ast"
	"github.com/microsoft/TypeScript/tsc/internal/printer"
	"github.com/microsoft/TypeScript/tsc/internal/transformers"
)

// EmitKind identifies which emit pipeline a Transformer is running in.
// ActionNoExport behaves differently between the two: it strips the
// export modifier in script output but keeps the declaration, while in
// declaration output it omits the declaration entirely, since a
// non-exported declaration has no public type surface to publish.
type EmitKind int

const (
	EmitKindScript EmitKind = iota
	EmitKindDeclaration
)

// Transformer applies @bouncer directives to top-level declarations:
// function, class, and variable statements, plus (in the declaration
// pipeline; by the time the script pipeline runs, type erasure has
// already removed these) interface, type alias, and enum declarations.
// Only direct top-level declarations are considered — nothing nested
// inside a function or class body, or inside a namespace, is visited.
type Transformer struct {
	transformers.Transformer
	options  *Options
	emitKind EmitKind
}

// NewTransformer creates a bouncer transformer for one emit pipeline. A
// nil options uses the defaults (no profiles configured; only "remove",
// "export", "export default", "export NAME" and "no-export" tags have any
// effect, and "no-export" only removes the export modifier since no
// profile can be resolved to distinguish pipelines further — this is
// unrelated to the emitKind parameter, which the caller always supplies).
func NewTransformer(emitContext *printer.EmitContext, options *Options, emitKind EmitKind) *transformers.Transformer {
	if options == nil {
		options = defaultOptions()
	}
	tx := &Transformer{options: options, emitKind: emitKind}
	return tx.NewTransformer(tx.visit, emitContext)
}

func (tx *Transformer) visit(node *ast.Node) *ast.Node {
	switch node.Kind {
	case ast.KindSourceFile:
		return tx.Visitor().VisitEachChild(node)
	case ast.KindFunctionDeclaration, ast.KindClassDeclaration, ast.KindVariableStatement,
		ast.KindInterfaceDeclaration, ast.KindTypeAliasDeclaration, ast.KindEnumDeclaration:
		return tx.apply(node)
	default:
		return node
	}
}

func (tx *Transformer) apply(node *ast.Node) *ast.Node {
	// Resolve against the original parse-tree node: JSDoc is attached
	// there, and a node reaching here may already be a tsc-p-synthesised
	// update from an earlier transformer (e.g. type erasure) — the JSDoc
	// cache is keyed by node identity, so a synthesised node's pointer
	// will not be present in it even though its NodeFlagsHasJSDoc flag
	// was copied over by the earlier Update call.
	original := tx.EmitContext().ParseNode(node)
	if original == nil {
		original = node
	}
	action := actionForNode(original, tx.options)

	switch action.Kind {
	case ActionRemove:
		return nil
	case ActionRelease:
		// dts-only trimming: JavaScript output is never affected by a
		// release channel, matching API Extractor's rollup semantics.
		if tx.emitKind == EmitKindDeclaration && tx.options.ReleaseRank >= 0 && releaseRank[action.Name] > tx.options.ReleaseRank {
			return nil
		}
		return node
	case ActionExport:
		return tx.setExport(node, true)
	case ActionNoExport:
		if tx.emitKind == EmitKindDeclaration {
			return nil
		}
		return tx.setExport(node, false)
	case ActionExportDefault:
		return tx.appendExportStatement(node, func(factory *printer.NodeFactory, nameRef *ast.IdentifierNode) *ast.Node {
			return factory.NewExportAssignment(nil, false /*isExportEquals*/, nil /*type*/, nameRef)
		})
	case ActionExportAs:
		return tx.appendExportStatement(node, func(factory *printer.NodeFactory, nameRef *ast.IdentifierNode) *ast.Node {
			specifier := factory.NewExportSpecifier(false /*isTypeOnly*/, nameRef, factory.NewIdentifier(action.Name))
			return factory.NewExportDeclaration(nil, false /*isTypeOnly*/, factory.NewNamedExports(factory.NewNodeList([]*ast.Node{specifier})), nil /*moduleSpecifier*/, nil /*attributes*/)
		})
	default:
		return node
	}
}

// setExport adds or removes the plain export modifier. Declarations
// marked "export default" are left untouched: toggling plain export on a
// default export has no well-defined meaning, and only ActionRemove
// applies to them.
func (tx *Transformer) setExport(node *ast.Node, want bool) *ast.Node {
	modifiers := node.Modifiers()
	if modifiers != nil && modifiers.ModifierFlags&ast.ModifierFlagsDefault != 0 {
		return node
	}
	newModifiers := rebuildModifiers(tx.Factory(), modifiers, want)
	if newModifiers == modifiers {
		return node
	}
	return updateDeclarationModifiers(tx.Factory(), node, newModifiers)
}

// appendExportStatement strips any export/default modifier from the
// original declaration and appends a new statement built by makeStatement
// from a fresh reference to the declaration's name — "export default
// <name>;" or "export { <name> as NewName };". If the declaration has no
// single unambiguous name (for example a variable statement declaring more
// than one binding), this is a no-op: the declaration is returned
// unchanged.
func (tx *Transformer) appendExportStatement(node *ast.Node, makeStatement func(factory *printer.NodeFactory, nameRef *ast.IdentifierNode) *ast.Node) *ast.Node {
	name, ok := declarationName(node)
	if !ok {
		return node
	}
	factory := tx.Factory()
	strippedModifiers := rebuildModifiers(factory, node.Modifiers(), false)
	updated := updateDeclarationModifiers(factory, node, strippedModifiers)
	statement := makeStatement(factory, factory.NewIdentifier(name.Text()))
	return factory.NewSyntaxList([]*ast.Node{updated, statement})
}

// declarationName returns the single unambiguous name a top-level
// declaration can be referenced by after emit, or false if it has none:
// a variable statement declaring anything other than exactly one plain
// identifier binding (a destructuring pattern, or more than one
// declarator) has no such name.
func declarationName(node *ast.Node) (*ast.IdentifierNode, bool) {
	var name *ast.Node
	switch node.Kind {
	case ast.KindFunctionDeclaration:
		name = node.AsFunctionDeclaration().Name()
	case ast.KindClassDeclaration:
		name = node.AsClassDeclaration().Name()
	case ast.KindInterfaceDeclaration:
		name = node.AsInterfaceDeclaration().Name()
	case ast.KindTypeAliasDeclaration:
		name = node.AsTypeAliasDeclaration().Name()
	case ast.KindEnumDeclaration:
		name = node.AsEnumDeclaration().Name()
	case ast.KindVariableStatement:
		declarations := node.AsVariableStatement().DeclarationList.AsVariableDeclarationList().Declarations.Nodes
		if len(declarations) != 1 {
			return nil, false
		}
		name = declarations[0].AsVariableDeclaration().Name()
	}
	if name != nil && ast.IsIdentifier(name) {
		return name, true
	}
	return nil, false
}

// updateDeclarationModifiers rebuilds node with newModifiers in place of
// its current modifier list, preserving every other field unchanged.
func updateDeclarationModifiers(factory *printer.NodeFactory, node *ast.Node, newModifiers *ast.ModifierList) *ast.Node {
	switch node.Kind {
	case ast.KindFunctionDeclaration:
		n := node.AsFunctionDeclaration()
		return factory.UpdateFunctionDeclaration(n, newModifiers, n.AsteriskToken, n.Name(), n.TypeParameters, n.Parameters, n.Type, n.FullSignature, n.Body)
	case ast.KindClassDeclaration:
		n := node.AsClassDeclaration()
		return factory.UpdateClassDeclaration(n, newModifiers, n.Name(), n.TypeParameters, n.HeritageClauses, n.Members)
	case ast.KindVariableStatement:
		n := node.AsVariableStatement()
		return factory.UpdateVariableStatement(n, newModifiers, n.DeclarationList)
	case ast.KindInterfaceDeclaration:
		n := node.AsInterfaceDeclaration()
		return factory.UpdateInterfaceDeclaration(n, newModifiers, n.Name(), n.TypeParameters, n.HeritageClauses, n.Members)
	case ast.KindTypeAliasDeclaration:
		n := node.AsTypeAliasDeclaration()
		return factory.UpdateTypeAliasDeclaration(n, newModifiers, n.Name(), n.TypeParameters, n.Type)
	case ast.KindEnumDeclaration:
		n := node.AsEnumDeclaration()
		return factory.UpdateEnumDeclaration(n, newModifiers, n.Name(), n.Members)
	default:
		return node
	}
}

// rebuildModifiers returns a modifier list with the export modifier added
// or removed (want, respectively true or false); removing also drops any
// default modifier, since "default" without "export" is not valid. If the
// requested state already holds, the input modifiers are returned
// unchanged (callers use this to detect a no-op). The export keyword is
// conventionally first when present, so a new one is prepended; removing
// preserves the relative order of everything else.
func rebuildModifiers(factory *printer.NodeFactory, modifiers *ast.ModifierList, want bool) *ast.ModifierList {
	var existing []*ast.Node
	if modifiers != nil {
		existing = modifiers.Nodes
	}

	hasExport := modifiers != nil && modifiers.ModifierFlags&ast.ModifierFlagsExport != 0
	if hasExport == want {
		return modifiers
	}

	var nodes []*ast.Node
	if want {
		nodes = make([]*ast.Node, 0, len(existing)+1)
		nodes = append(nodes, factory.NewModifier(ast.KindExportKeyword))
		nodes = append(nodes, existing...)
	} else {
		nodes = make([]*ast.Node, 0, len(existing))
		for _, modifier := range existing {
			if modifier.Kind != ast.KindExportKeyword && modifier.Kind != ast.KindDefaultKeyword {
				nodes = append(nodes, modifier)
			}
		}
	}
	return factory.NewModifierList(nodes)
}
