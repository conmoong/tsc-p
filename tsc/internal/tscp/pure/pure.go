// Package pure implements @conmoong/pure, tsc-p's tree-shakability
// annotator. A `@pure` JSDoc tag on a top-level variable statement makes
// every call and `new` expression evaluated by its initialisers carry a
// /*#__PURE__*/ annotation in the emitted JavaScript, telling bundlers and
// minifiers the initialiser may be dropped when its result is unused —
// the still-open microsoft/TypeScript#13721, as an explicit per-statement
// marker rather than a global heuristic.
//
//	/** @pure */
//	export const registry = createRegistry(defaults());
//
// Only the module-load-time "spine" of an initialiser is annotated:
// nothing inside function or class bodies (those run later, not at module
// evaluation). The plugin never proves purity — the tag is the author's
// explicit assertion, in the same philosophy as bouncer's directives.
package pure

import (
	"github.com/microsoft/TypeScript/tsc/internal/ast"
	"github.com/microsoft/TypeScript/tsc/internal/printer"
	"github.com/microsoft/TypeScript/tsc/internal/transformers"
	"github.com/microsoft/TypeScript/tsc/internal/tscp/hooks"
)

// PluginName is the compilerOptions.plugins entry name.
const PluginName = "@conmoong/pure"

// Options is the plugin configuration; presence of the plugins entry
// activates it.
type Options struct{}

// OptionsFromConfig returns non-nil when the plugin is configured.
func OptionsFromConfig(raw any) *Options {
	if _, ok := hooks.FindPluginEntry(raw, PluginName); !ok {
		return nil
	}
	return &Options{}
}

type plugin struct {
	options *Options
}

// NewPlugin creates the emit plugin.
func NewPlugin(options *Options) hooks.EmitPlugin {
	return &plugin{options: options}
}

func (p *plugin) Name() string { return PluginName }

func (p *plugin) ScriptTransformer(emitContext *printer.EmitContext, host printer.EmitHost) *transformers.Transformer {
	return NewTransformer(emitContext)
}

func (p *plugin) DeclarationTransformer(emitContext *printer.EmitContext, host printer.EmitHost) *transformers.Transformer {
	// Annotations are a JavaScript-output concern only.
	return nil
}

// Transformer annotates @pure-tagged top-level variable statements.
type Transformer struct {
	transformers.Transformer
}

// NewTransformer creates the transformer for one file's script emit.
func NewTransformer(emitContext *printer.EmitContext) *transformers.Transformer {
	tx := &Transformer{}
	return tx.NewTransformer(tx.visit, emitContext)
}

func (tx *Transformer) visit(node *ast.Node) *ast.Node {
	switch node.Kind {
	case ast.KindSourceFile:
		return tx.Visitor().VisitEachChild(node)
	case ast.KindVariableStatement:
		if tx.hasPureTag(node) {
			for _, declarator := range node.AsVariableStatement().DeclarationList.AsVariableDeclarationList().Declarations.Nodes {
				if initializer := declarator.AsVariableDeclaration().Initializer; initializer != nil {
					tx.annotateSpine(initializer)
				}
			}
		}
		return node
	default:
		// Top-level statements only; nothing nested is considered.
		return node
	}
}

// hasPureTag reports whether the statement's JSDoc carries a `@pure` tag.
// JSDoc must be read off the true parse-tree original: the jsdoc cache is
// keyed by node pointer and earlier transformers synthesise new nodes.
func (tx *Transformer) hasPureTag(node *ast.Node) bool {
	original := tx.EmitContext().ParseNode(node)
	if original == nil {
		original = node
	}
	for _, jsdoc := range original.JSDoc(nil) {
		tags := jsdoc.AsJSDoc().Tags
		if tags == nil {
			continue
		}
		for _, tag := range tags.Nodes {
			if ast.IsJSDocUnknownTag(tag) && tag.TagName() != nil && tag.TagName().Text() == "pure" {
				return true
			}
		}
	}
	return false
}

// annotateSpine adds /*#__PURE__*/ to every call and new expression on the
// module-evaluation spine of an expression: the parts that execute when
// the initialiser runs. Function-like and class bodies are skipped — code
// inside them runs later, and annotating it would assert purity of calls
// the tag's author never looked at.
func (tx *Transformer) annotateSpine(expression *ast.Node) {
	var walk func(node *ast.Node) bool
	walk = func(node *ast.Node) bool {
		if ast.IsFunctionLike(node) || ast.IsClassLike(node) {
			return false
		}
		if node.Kind == ast.KindCallExpression || node.Kind == ast.KindNewExpression {
			tx.EmitContext().AddSyntheticLeadingComment(node, ast.KindMultiLineCommentTrivia, "#__PURE__", false)
		}
		node.ForEachChild(walk)
		return false
	}
	walk(expression)
}
