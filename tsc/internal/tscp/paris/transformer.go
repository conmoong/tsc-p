package paris

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/microsoft/TypeScript/tsc/internal/ast"
	"github.com/microsoft/TypeScript/tsc/internal/printer"
	"github.com/microsoft/TypeScript/tsc/internal/transformers"
)

// Transformer applies paris conditions and value substitutions in the
// script emit pipeline. Modules import the runtime package; the transformer
// freezes those calls at build time:
//
//   - a statement-position condition call compiles to the callback body as
//     a plain block statement (condition true) or to nothing (false);
//     nested condition calls inside a surviving body are processed
//     recursively, and a false outer branch prunes its inner calls
//     unevaluated (C #ifdef-guard semantics);
//   - value(name) calls in any expression position are replaced by the
//     configured value as a literal;
//   - everything the design defines as misuse is a build-failing
//     diagnostic, never a silent fallback: non-literal arguments,
//     condition calls in expression position, non-inline callbacks,
//     callback parameters/async/generators, return or var in a body,
//     type-mismatched comparisons, RE2-incompatible regexes, and any
//     evaluated non-Def predicate on an undefined variable.
//
// The runtime import is removed when every reference to it was transformed.
type Transformer struct {
	transformers.Transformer
	plugin *Plugin
	file   *ast.SourceFile

	// Local binding names from `import ... from "@conmoong/paris"`.
	named      map[string]predicate // named imports: local name -> predicate
	namespaces map[string]bool      // default and namespace import local names
}

// NewTransformer creates a paris transformer for one file's script emit.
func NewTransformer(emitContext *printer.EmitContext, plugin *Plugin) *transformers.Transformer {
	tx := &Transformer{plugin: plugin}
	return tx.NewTransformer(tx.visit, emitContext)
}

func (tx *Transformer) visit(node *ast.Node) *ast.Node {
	switch node.Kind {
	case ast.KindSourceFile:
		return tx.visitSourceFile(node)
	case ast.KindExpressionStatement:
		return tx.visitExpressionStatement(node)
	case ast.KindCallExpression:
		return tx.visitCallExpression(node)
	default:
		return tx.Visitor().VisitEachChild(node)
	}
}

func (tx *Transformer) visitSourceFile(node *ast.Node) *ast.Node {
	file := node.AsSourceFile()
	tx.file = file
	tx.named = nil
	tx.namespaces = nil
	for _, statement := range file.Statements.Nodes {
		tx.collectImport(statement)
	}
	if len(tx.named) == 0 && len(tx.namespaces) == 0 {
		return node
	}
	result := tx.Visitor().VisitEachChild(node)
	return tx.elideUnusedImports(result)
}

// collectImport records the local bindings introduced by an import of the
// runtime package — an ESM import, or a CommonJS `require()` (TypeScript
// source is free to use either, even in a project that otherwise emits
// ESM, so both must be recognised the same way).
func (tx *Transformer) collectImport(statement *ast.Node) {
	if ast.IsImportDeclaration(statement) {
		tx.collectESMImport(statement)
		return
	}
	if ast.IsVariableStatement(statement) {
		tx.collectRequireImport(statement)
	}
}

func (tx *Transformer) collectESMImport(statement *ast.Node) {
	declaration := statement.AsImportDeclaration()
	if !ast.IsStringLiteral(declaration.ModuleSpecifier) || declaration.ModuleSpecifier.Text() != PluginName {
		return
	}
	clause := declaration.ImportClause
	if clause == nil {
		return
	}
	importClause := clause.AsImportClause()
	if name := importClause.Name(); name != nil {
		tx.addNamespace(name.Text())
	}
	if bindings := importClause.NamedBindings; bindings != nil {
		switch bindings.Kind {
		case ast.KindNamespaceImport:
			tx.addNamespace(bindings.AsNamespaceImport().Name().Text())
		case ast.KindNamedImports:
			for _, element := range bindings.AsNamedImports().Elements.Nodes {
				specifier := element.AsImportSpecifier()
				exported := specifier.Name().Text()
				if specifier.PropertyName != nil {
					exported = specifier.PropertyName.Text()
				}
				tx.addNamed(exported, specifier.Name().Text())
			}
		}
	}
}

// collectRequireImport recognises `const paris = require("@conmoong/paris")`
// (whole-module binding, treated like a namespace import) and
// `const { ifDef, ifEq: eq } = require("@conmoong/paris")` (named bindings,
// property-renamed or not) — the two require() shapes source actually
// writes; each declarator in the statement is considered independently, so
// `const paris = require(...), other = require("x");` only recognises the
// matching declarator.
func (tx *Transformer) collectRequireImport(statement *ast.Node) {
	for _, declarator := range statement.AsVariableStatement().DeclarationList.AsVariableDeclarationList().Declarations.Nodes {
		declaration := declarator.AsVariableDeclaration()
		if !isParisRequireDeclaration(declaration) {
			continue
		}
		name := declaration.Name()
		switch name.Kind {
		case ast.KindIdentifier:
			tx.addNamespace(name.Text())
		case ast.KindObjectBindingPattern:
			for _, element := range name.AsBindingPattern().Elements.Nodes {
				binding := element.AsBindingElement()
				if binding.DotDotDotToken != nil {
					continue // `const { ...rest } = require(...)`: not a named paris binding.
				}
				localName := binding.Name()
				if localName == nil || localName.Kind != ast.KindIdentifier {
					continue // A nested destructuring pattern; not supported.
				}
				exported := localName.Text()
				if binding.PropertyName != nil {
					if binding.PropertyName.Kind != ast.KindIdentifier && binding.PropertyName.Kind != ast.KindStringLiteral {
						continue // A computed or numeric property name can't name a paris export.
					}
					exported = binding.PropertyName.Text()
				}
				tx.addNamed(exported, localName.Text())
			}
		}
	}
}

// isParisRequireDeclaration reports whether declaration is initialized to
// `require("@conmoong/paris")`.
func isParisRequireDeclaration(declaration *ast.VariableDeclaration) bool {
	if declaration.Initializer == nil || !ast.IsRequireCall(declaration.Initializer, true /*requireStringLiteralLikeArgument*/) {
		return false
	}
	argument := declaration.Initializer.AsCallExpression().Arguments.Nodes[0]
	return argument.Text() == PluginName
}

func (tx *Transformer) addNamespace(localName string) {
	if tx.namespaces == nil {
		tx.namespaces = make(map[string]bool)
	}
	tx.namespaces[localName] = true
}

func (tx *Transformer) addNamed(exportedName string, localName string) {
	if pred, ok := predicateNames[exportedName]; ok {
		if tx.named == nil {
			tx.named = make(map[string]predicate)
		}
		tx.named[localName] = pred
	}
}

// classifyCall reports whether call is a paris call and which predicate it
// names.
func (tx *Transformer) classifyCall(call *ast.CallExpression) (predicate, bool) {
	callee := call.Expression
	switch callee.Kind {
	case ast.KindIdentifier:
		pred, ok := tx.named[callee.Text()]
		return pred, ok
	case ast.KindPropertyAccessExpression:
		access := callee.AsPropertyAccessExpression()
		if access.Expression.Kind != ast.KindIdentifier || !tx.namespaces[access.Expression.Text()] {
			return predNone, false
		}
		pred, ok := predicateNames[access.Name().Text()]
		return pred, ok
	default:
		return predNone, false
	}
}

func (tx *Transformer) visitExpressionStatement(node *ast.Node) *ast.Node {
	expression := node.AsExpressionStatement().Expression
	if ast.IsCallExpression(expression) {
		if pred, ok := tx.classifyCall(expression.AsCallExpression()); ok && pred != predValue {
			return tx.applyCondition(node, expression.AsCallExpression(), pred)
		}
	}
	return tx.Visitor().VisitEachChild(node)
}

func (tx *Transformer) visitCallExpression(node *ast.Node) *ast.Node {
	pred, ok := tx.classifyCall(node.AsCallExpression())
	if !ok {
		return tx.Visitor().VisitEachChild(node)
	}
	if pred != predValue {
		// A condition call reached outside statement position: statement
		// calls are consumed by visitExpressionStatement before descending.
		tx.error(node, "%s(...) must be a standalone statement; its result cannot be used as a value", pred.name())
		return node
	}
	return tx.applyValue(node)
}

// applyCondition transforms one statement-position condition call.
func (tx *Transformer) applyCondition(statement *ast.Node, call *ast.CallExpression, pred predicate) *ast.Node {
	arguments := call.Arguments.Nodes
	if len(arguments) != pred.argCount() {
		tx.error(statement, "%s expects %d arguments, got %d", pred.name(), pred.argCount(), len(arguments))
		return statement
	}
	varName, ok := tx.stringLiteral(arguments[0])
	if !ok {
		tx.error(statement, "%s: the variable name must be a string literal", pred.name())
		return statement
	}

	var arg *comparand
	var pattern *regexpOrNil
	switch pred {
	case predIfEq, predIfNotEq:
		literal, errText := literalComparand(arguments[1])
		if errText != "" {
			tx.error(statement, "%s(%q): %s", pred.name(), varName, errText)
			return statement
		}
		arg = literal
	case predIfGt, predIfGte, predIfLt, predIfLte:
		literal, errText := literalComparand(arguments[1])
		if errText != "" {
			tx.error(statement, "%s(%q): %s", pred.name(), varName, errText)
			return statement
		}
		if literal.kind != KindNumber {
			tx.error(statement, "%s(%q): the bound must be a number literal", pred.name(), varName)
			return statement
		}
		arg = literal
	case predIfMatch, predIfNotMatch:
		compiled, errText := regexLiteral(arguments[1])
		if errText != "" {
			tx.error(statement, "%s(%q): %s", pred.name(), varName, errText)
			return statement
		}
		pattern = compiled
	}

	callback := arguments[len(arguments)-1]
	body, errText := tx.callbackBlock(callback)
	if errText != "" {
		tx.error(statement, "%s(%q): %s", pred.name(), varName, errText)
		return statement
	}

	var value *Value
	if configured, ok := tx.plugin.options.Values[varName]; ok {
		value = &configured
	}
	result, evalError := evaluate(pred, varName, value, arg, pattern.get())
	if evalError != "" {
		tx.error(statement, "%s", evalError)
		return statement
	}
	if !result {
		// Pruned branch: inner calls are removed unevaluated.
		return nil
	}
	block := tx.Factory().NewBlock(body, true)
	tx.EmitContext().SetOriginal(block, statement)
	block.Loc = statement.Loc
	tx.EmitContext().AssignCommentAndSourceMapRanges(block, statement)
	// Visit the surviving body for nested paris calls.
	return tx.Visitor().VisitEachChild(block)
}

// callbackBlock validates the inline callback and returns its statements.
func (tx *Transformer) callbackBlock(callback *ast.Node) (*ast.StatementList, string) {
	var body *ast.Node
	switch callback.Kind {
	case ast.KindFunctionExpression:
		fn := callback.AsFunctionExpression()
		if fn.AsteriskToken != nil {
			return nil, "the callback must not be a generator"
		}
		if len(fn.Parameters.Nodes) > 0 {
			return nil, "the callback must take no parameters"
		}
		if fn.Modifiers() != nil {
			return nil, "the callback must not be async"
		}
		body = fn.Body
	case ast.KindArrowFunction:
		fn := callback.AsArrowFunction()
		if len(fn.Parameters.Nodes) > 0 {
			return nil, "the callback must take no parameters"
		}
		if fn.Modifiers() != nil {
			return nil, "the callback must not be async"
		}
		body = fn.Body
	default:
		return nil, "the callback must be an inline function or arrow expression"
	}
	var statements *ast.StatementList
	if body.Kind == ast.KindBlock {
		statements = body.AsBlock().Statements
	} else {
		// Arrow expression body: a single expression statement.
		expressionStatement := tx.Factory().NewExpressionStatement(body)
		tx.EmitContext().SetOriginal(expressionStatement, body)
		expressionStatement.Loc = body.Loc
		statements = tx.Factory().NewNodeList([]*ast.Node{expressionStatement})
	}
	if errText := scanBody(statements.Nodes); errText != "" {
		return nil, errText
	}
	return statements, ""
}

// scanBody rejects return and var at any depth that belongs to the
// callback itself (function-like boundaries and class bodies are skipped:
// their return/var semantics are unchanged by block emit).
func scanBody(statements []*ast.Node) string {
	var errText string
	var walk func(node *ast.Node) bool
	walk = func(node *ast.Node) bool {
		if errText != "" {
			return true
		}
		switch node.Kind {
		case ast.KindReturnStatement:
			errText = "the callback body must not contain return (in a block it would return from the enclosing function)"
			return true
		case ast.KindVariableStatement:
			if node.AsVariableStatement().DeclarationList.Flags&(ast.NodeFlagsLet|ast.NodeFlagsConst) == 0 {
				errText = "the callback body must not declare var (it would hoist out of the block); use let or const"
				return true
			}
		}
		if ast.IsFunctionLike(node) || ast.IsClassLike(node) {
			return false
		}
		node.ForEachChild(walk)
		return false
	}
	for _, statement := range statements {
		walk(statement)
	}
	return errText
}

// applyValue replaces value(name) with the configured value as a literal.
func (tx *Transformer) applyValue(node *ast.Node) *ast.Node {
	call := node.AsCallExpression()
	if len(call.Arguments.Nodes) != 1 {
		tx.error(node, "value expects exactly 1 argument, got %d", len(call.Arguments.Nodes))
		return node
	}
	varName, ok := tx.stringLiteral(call.Arguments.Nodes[0])
	if !ok {
		tx.error(node, "value: the variable name must be a string literal")
		return node
	}
	value, defined := tx.plugin.options.Values[varName]
	if !defined {
		tx.error(node, "value(%q): variable is not defined", varName)
		return node
	}
	literal, errText := tx.expressionForValue(&value)
	if errText != "" {
		tx.error(node, "value(%q): %s", varName, errText)
		return node
	}
	tx.EmitContext().SetOriginal(literal, node)
	literal.Loc = node.Loc
	tx.EmitContext().AssignCommentAndSourceMapRanges(literal, node)
	return literal
}

func (tx *Transformer) stringLiteral(node *ast.Node) (string, bool) {
	if node.Kind == ast.KindStringLiteral || node.Kind == ast.KindNoSubstitutionTemplateLiteral {
		return node.Text(), true
	}
	return "", false
}

// literalComparand reads a string/number/boolean literal argument,
// accepting a leading unary minus on numbers.
func literalComparand(node *ast.Node) (*comparand, string) {
	negative := false
	if node.Kind == ast.KindPrefixUnaryExpression {
		unary := node.AsPrefixUnaryExpression()
		if unary.Operator == ast.KindMinusToken {
			negative = true
			node = unary.Operand
		}
	}
	switch node.Kind {
	case ast.KindStringLiteral, ast.KindNoSubstitutionTemplateLiteral:
		if negative {
			return nil, "the comparison value must be a literal"
		}
		return &comparand{kind: KindString, str: node.Text()}, ""
	case ast.KindNumericLiteral:
		number, err := parseNumericLiteral(node.Text())
		if err != "" {
			return nil, err
		}
		if negative {
			number = -number
		}
		return &comparand{kind: KindNumber, num: number}, ""
	case ast.KindTrueKeyword:
		return &comparand{kind: KindBool, b: true}, ""
	case ast.KindFalseKeyword:
		return &comparand{kind: KindBool, b: false}, ""
	default:
		return nil, "the comparison value must be a string, number, or boolean literal"
	}
}

// regexpOrNil lets a nil pattern flow through evaluate without a typed-nil
// pointer footgun.
type regexpOrNil struct {
	compiled *regexp.Regexp
}

func (r *regexpOrNil) get() *regexp.Regexp {
	if r == nil {
		return nil
	}
	return r.compiled
}

func regexLiteral(node *ast.Node) (*regexpOrNil, string) {
	if node.Kind != ast.KindRegularExpressionLiteral {
		return nil, "the pattern must be a regex literal"
	}
	text := node.Text()
	lastSlash := strings.LastIndexByte(text, '/')
	if len(text) < 2 || text[0] != '/' || lastSlash <= 0 {
		return nil, fmt.Sprintf("malformed regex literal %q", text)
	}
	pattern := text[1:lastSlash]
	flags := text[lastSlash+1:]
	compiled, errText := compileJSRegex(pattern, flags)
	if errText != "" {
		return nil, errText
	}
	return &regexpOrNil{compiled: compiled}, ""
}

// error records a build-failing diagnostic anchored at node.
func (tx *Transformer) error(node *ast.Node, format string, args ...any) {
	tx.plugin.addDiagnostic(tx.file, node, fmt.Sprintf(format, args...))
}

// elideUnusedImports removes runtime-package imports whose bindings have no
// remaining references in the transformed file. Counting is name-based and
// conservative: a shadowing identifier keeps the import, which is harmless.
func (tx *Transformer) elideUnusedImports(node *ast.Node) *ast.Node {
	file := node.AsSourceFile()
	remaining := make(map[string]int)
	for name := range tx.named {
		remaining[name] = 0
	}
	for name := range tx.namespaces {
		remaining[name] = 0
	}
	var count func(n *ast.Node) bool
	count = func(n *ast.Node) bool {
		if ast.IsImportDeclaration(n) {
			specifier := n.AsImportDeclaration().ModuleSpecifier
			if ast.IsStringLiteral(specifier) && specifier.Text() == PluginName {
				return false
			}
		}
		if n.Kind == ast.KindVariableDeclaration && isParisRequireDeclaration(n.AsVariableDeclaration()) {
			// Skip this declarator's binding pattern (the introduction of
			// the names, not a use of them) and its require() call (whose
			// only identifier is "require" itself, never tracked). Sibling
			// declarators in the same statement are unaffected — count
			// only skips this one node's subtree.
			return false
		}
		if n.Kind == ast.KindIdentifier {
			if _, tracked := remaining[n.Text()]; tracked {
				remaining[n.Text()]++
			}
		}
		n.ForEachChild(count)
		return false
	}
	for _, statement := range file.Statements.Nodes {
		count(statement)
	}

	statements := make([]*ast.Node, 0, len(file.Statements.Nodes))
	changed := false
	for _, statement := range file.Statements.Nodes {
		if tx.isFullyUnusedParisImport(statement, remaining) || tx.isFullyUnusedParisRequire(statement, remaining) {
			changed = true
			continue
		}
		statements = append(statements, statement)
	}
	if !changed {
		return node
	}
	statementList := tx.Factory().NewNodeList(statements)
	statementList.Loc = file.Statements.Loc
	result := tx.Factory().UpdateSourceFile(file, statementList, file.EndOfFileToken)
	return result
}

func (tx *Transformer) isFullyUnusedParisImport(statement *ast.Node, remaining map[string]int) bool {
	if !ast.IsImportDeclaration(statement) {
		return false
	}
	declaration := statement.AsImportDeclaration()
	if !ast.IsStringLiteral(declaration.ModuleSpecifier) || declaration.ModuleSpecifier.Text() != PluginName {
		return false
	}
	clause := declaration.ImportClause
	if clause == nil {
		// Bare `import "@conmoong/paris"`: side-effect-free runtime shim;
		// keep it (nothing was transformed through it).
		return false
	}
	importClause := clause.AsImportClause()
	if name := importClause.Name(); name != nil && remaining[name.Text()] > 0 {
		return false
	}
	if bindings := importClause.NamedBindings; bindings != nil {
		switch bindings.Kind {
		case ast.KindNamespaceImport:
			if remaining[bindings.AsNamespaceImport().Name().Text()] > 0 {
				return false
			}
		case ast.KindNamedImports:
			for _, element := range bindings.AsNamedImports().Elements.Nodes {
				local := element.AsImportSpecifier().Name().Text()
				if _, tracked := tx.named[local]; !tracked {
					// An import of something we don't recognise: keep.
					return false
				}
				if remaining[local] > 0 {
					return false
				}
			}
		}
	}
	return true
}

// isFullyUnusedParisRequire mirrors isFullyUnusedParisImport for the
// `require()` form. Only a single-declarator statement
// (`const paris = require("@conmoong/paris");`) is ever removed — a
// statement with other declarators is left untouched entirely, even if its
// paris declarator alone is unused, to avoid partial-statement surgery.
func (tx *Transformer) isFullyUnusedParisRequire(statement *ast.Node, remaining map[string]int) bool {
	if !ast.IsVariableStatement(statement) {
		return false
	}
	declarations := statement.AsVariableStatement().DeclarationList.AsVariableDeclarationList().Declarations.Nodes
	if len(declarations) != 1 {
		return false
	}
	declaration := declarations[0].AsVariableDeclaration()
	if !isParisRequireDeclaration(declaration) {
		return false
	}
	name := declaration.Name()
	switch name.Kind {
	case ast.KindIdentifier:
		return remaining[name.Text()] == 0
	case ast.KindObjectBindingPattern:
		for _, element := range name.AsBindingPattern().Elements.Nodes {
			binding := element.AsBindingElement()
			if binding.DotDotDotToken != nil {
				// A rest element we never recognised as a binding: keep,
				// consistent with the "unrecognised binding => keep" rule
				// named imports already follow.
				return false
			}
			localName := binding.Name()
			if localName == nil || localName.Kind != ast.KindIdentifier {
				return false
			}
			if _, tracked := tx.named[localName.Text()]; !tracked {
				return false
			}
			if remaining[localName.Text()] > 0 {
				return false
			}
		}
		return true
	default:
		return false
	}
}
