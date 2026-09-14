package pathrewrite

import (
	"slices"

	"github.com/microsoft/TypeScript/tsc/internal/ast"
	"github.com/microsoft/TypeScript/tsc/internal/printer"
	"github.com/microsoft/TypeScript/tsc/internal/transformers"
)

// PathRewriteTransformer routes every supported module-specifier syntax
// through an injected ModuleSpecifierRewriter. It performs a purely
// syntactic walk: no resolver access, no compiler-option interpretation.
// Nodes whose specifier the rewriter leaves unchanged are returned as-is,
// so an identity rewriter yields an identical AST.
type PathRewriteTransformer struct {
	transformers.Transformer
	rewriter          ModuleSpecifierRewriter
	emitKind          EmitKind
	currentSourceFile *ast.SourceFile
}

// NewTransformer creates a module-specifier rewrite transformer for one
// emit pipeline. A new transformer must be created for each file transform;
// the shared rewriter must be safe for concurrent use.
func NewTransformer(emitContext *printer.EmitContext, rewriter ModuleSpecifierRewriter, emitKind EmitKind) *transformers.Transformer {
	tx := &PathRewriteTransformer{rewriter: rewriter, emitKind: emitKind}
	return tx.NewTransformer(tx.visit, emitContext)
}

func (tx *PathRewriteTransformer) visit(node *ast.Node) *ast.Node {
	switch node.Kind {
	case ast.KindSourceFile:
		savedCurrentSourceFile := tx.currentSourceFile
		tx.currentSourceFile = node.AsSourceFile()
		node = tx.Visitor().VisitEachChild(node)
		tx.currentSourceFile = savedCurrentSourceFile
		return node
	case ast.KindImportDeclaration:
		n := node.AsImportDeclaration()
		syntax := SyntaxImportDeclaration
		if n.ImportClause == nil {
			syntax = SyntaxSideEffectImport
		}
		specifier := tx.rewriteSpecifier(n.ModuleSpecifier, syntax)
		if specifier == n.ModuleSpecifier {
			return node
		}
		return tx.Factory().UpdateImportDeclaration(n, n.Modifiers(), n.ImportClause, specifier, n.Attributes)
	case ast.KindExportDeclaration:
		n := node.AsExportDeclaration()
		if n.ModuleSpecifier == nil {
			return node
		}
		specifier := tx.rewriteSpecifier(n.ModuleSpecifier, SyntaxExportDeclaration)
		if specifier == n.ModuleSpecifier {
			return node
		}
		return tx.Factory().UpdateExportDeclaration(n, n.Modifiers(), n.IsTypeOnly, n.ExportClause, specifier, n.Attributes)
	case ast.KindImportEqualsDeclaration:
		if !ast.IsExternalModuleImportEqualsDeclaration(node) {
			return node
		}
		n := node.AsImportEqualsDeclaration()
		ref := n.ModuleReference.AsExternalModuleReference()
		expression := tx.rewriteSpecifier(ref.Expression, SyntaxImportEquals)
		if expression == ref.Expression {
			return node
		}
		return tx.Factory().UpdateImportEqualsDeclaration(n, n.Modifiers(), n.IsTypeOnly, n.Name(), tx.Factory().UpdateExternalModuleReference(ref, expression))
	case ast.KindImportType:
		// Visit children first: type arguments may contain nested import types.
		node = tx.Visitor().VisitEachChild(node)
		if !ast.IsLiteralImportTypeNode(node) {
			return node
		}
		n := node.AsImportTypeNode()
		literalType := n.Argument.AsLiteralTypeNode()
		specifier := tx.rewriteSpecifier(literalType.Literal, SyntaxImportType)
		if specifier == literalType.Literal {
			return node
		}
		return tx.Factory().UpdateImportTypeNode(n, n.IsTypeOf, tx.Factory().UpdateLiteralTypeNode(literalType, specifier), n.Attributes, n.Qualifier, n.TypeArguments)
	case ast.KindCallExpression:
		// Visit children first: arguments may contain nested import calls.
		node = tx.Visitor().VisitEachChild(node)
		var syntax SpecifierSyntax
		switch {
		case ast.IsImportCall(node) && len(node.AsCallExpression().Arguments.Nodes) > 0:
			syntax = SyntaxImportCall
		case ast.IsRequireCall(node, true /*requireStringLiteralLikeArgument*/):
			// Purely syntactic match; `require` may be a local binding
			// rather than Node.js require. The rewriter decides.
			syntax = SyntaxRequireCall
		default:
			return node
		}
		n := node.AsCallExpression()
		argument := n.Arguments.Nodes[0]
		specifier := tx.rewriteSpecifier(argument, syntax)
		if specifier == argument {
			return node
		}
		argumentNodes := slices.Clone(n.Arguments.Nodes)
		argumentNodes[0] = specifier
		arguments := tx.Factory().NewNodeList(argumentNodes)
		arguments.Loc = n.Arguments.Loc
		return tx.Factory().UpdateCallExpression(n, n.Expression, n.QuestionDotToken, n.TypeArguments, arguments, n.Flags)
	default:
		return tx.Visitor().VisitEachChild(node)
	}
}

// rewriteSpecifier offers a specifier node to the rewriter and returns a
// replacement string literal when a change is requested. Comment and
// source-map ranges are carried over from the original node. Non-string
// specifiers (for example a computed dynamic-import argument) are never
// offered for rewriting.
func (tx *PathRewriteTransformer) rewriteSpecifier(node *ast.Node, syntax SpecifierSyntax) *ast.Node {
	if node == nil || !ast.IsStringLiteral(node) {
		return node
	}
	// Prefer the original parse-tree node: it has an intact parent chain,
	// which resolution-mode lookups rely on. Declaration emit may present
	// fully synthesised literals with no original; pass those through so a
	// rewriter can still try (guardedly) to work with them.
	specifierNode := tx.EmitContext().ParseNode(node)
	if specifierNode == nil {
		specifierNode = node
	}
	context := RewriteContext{
		EmitKind:      tx.emitKind,
		Syntax:        syntax,
		SourceFile:    tx.currentSourceFile,
		SpecifierNode: specifierNode,
	}
	replacement, changed := tx.rewriter.RewriteModuleSpecifier(context, node.Text())
	if !changed || replacement == node.Text() {
		return node
	}
	updated := tx.Factory().NewStringLiteral(replacement, node.AsStringLiteral().TokenFlags)
	tx.EmitContext().SetOriginal(updated, node)
	tx.EmitContext().AssignCommentAndSourceMapRanges(updated, node)
	return updated
}
