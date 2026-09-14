package paris

import (
	"sort"
	"strconv"
	"strings"

	"github.com/microsoft/TypeScript/tsc/internal/ast"
	"github.com/microsoft/TypeScript/tsc/internal/collections"
)

// parseNumericLiteral parses a TypeScript numeric literal's text (which may
// carry separators and non-decimal prefixes).
func parseNumericLiteral(text string) (float64, string) {
	text = strings.ReplaceAll(text, "_", "")
	if number, err := strconv.ParseFloat(text, 64); err == nil {
		return number, ""
	}
	if integer, err := strconv.ParseInt(text, 0, 64); err == nil {
		return float64(integer), ""
	}
	return 0, "unsupported numeric literal " + text
}

// expressionForValue builds the literal expression a value(name) call is
// replaced with.
func (tx *Transformer) expressionForValue(value *Value) (*ast.Node, string) {
	switch value.Kind {
	case KindString:
		return tx.Factory().NewStringLiteral(value.Str, ast.TokenFlagsNone), ""
	case KindNumber:
		return tx.numberExpression(value.Num), ""
	case KindBool:
		return tx.boolExpression(value.Bool), ""
	case KindJSON:
		return tx.jsonExpression(value.JSON)
	case KindBytes:
		return tx.bytesExpression(value), ""
	}
	return nil, "internal: unhandled value kind"
}

func (tx *Transformer) numberExpression(number float64) *ast.Node {
	factory := tx.Factory()
	text := strconv.FormatFloat(number, 'f', -1, 64)
	if negative := strings.HasPrefix(text, "-"); negative {
		return factory.NewPrefixUnaryExpression(ast.KindMinusToken, factory.NewNumericLiteral(text[1:], ast.TokenFlagsNone))
	}
	return factory.NewNumericLiteral(text, ast.TokenFlagsNone)
}

func (tx *Transformer) boolExpression(b bool) *ast.Node {
	if b {
		return tx.Factory().NewKeywordExpression(ast.KindTrueKeyword)
	}
	return tx.Factory().NewKeywordExpression(ast.KindFalseKeyword)
}

// jsonExpression converts a parsed JSON value to a literal expression.
// Object keys are emitted as string literals; plain Go maps are sorted for
// deterministic output (tsconfig objects arrive as OrderedMap and keep
// their written order).
func (tx *Transformer) jsonExpression(value any) (*ast.Node, string) {
	factory := tx.Factory()
	switch v := value.(type) {
	case nil:
		return factory.NewKeywordExpression(ast.KindNullKeyword), ""
	case string:
		return factory.NewStringLiteral(v, ast.TokenFlagsNone), ""
	case float64:
		return tx.numberExpression(v), ""
	case bool:
		return tx.boolExpression(v), ""
	case []any:
		elements := make([]*ast.Node, 0, len(v))
		for _, element := range v {
			expression, errText := tx.jsonExpression(element)
			if errText != "" {
				return nil, errText
			}
			elements = append(elements, expression)
		}
		return factory.NewArrayLiteralExpression(factory.NewNodeList(elements), false), ""
	case map[string]any:
		keys := make([]string, 0, len(v))
		for key := range v {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		properties := make([]*ast.Node, 0, len(keys))
		for _, key := range keys {
			property, errText := tx.jsonProperty(key, v[key])
			if errText != "" {
				return nil, errText
			}
			properties = append(properties, property)
		}
		return factory.NewObjectLiteralExpression(factory.NewNodeList(properties), false), ""
	case *collections.OrderedMap[string, any]:
		properties := make([]*ast.Node, 0, v.Size())
		for key, entry := range v.Entries() {
			property, errText := tx.jsonProperty(key, entry)
			if errText != "" {
				return nil, errText
			}
			properties = append(properties, property)
		}
		return factory.NewObjectLiteralExpression(factory.NewNodeList(properties), false), ""
	default:
		return nil, "value contains something that is not JSON"
	}
}

func (tx *Transformer) jsonProperty(key string, value any) (*ast.Node, string) {
	factory := tx.Factory()
	expression, errText := tx.jsonExpression(value)
	if errText != "" {
		return nil, errText
	}
	name := factory.NewStringLiteral(key, ast.TokenFlagsNone)
	return factory.NewPropertyAssignment(nil, name, nil, nil, expression), ""
}

// bytesExpression emits new Uint8Array([...]) or Buffer.from([...]).
func (tx *Transformer) bytesExpression(value *Value) *ast.Node {
	factory := tx.Factory()
	elements := make([]*ast.Node, 0, len(value.Bytes))
	for _, b := range value.Bytes {
		elements = append(elements, factory.NewNumericLiteral(strconv.Itoa(int(b)), ast.TokenFlagsNone))
	}
	array := factory.NewArrayLiteralExpression(factory.NewNodeList(elements), false)
	arguments := factory.NewNodeList([]*ast.Node{array})
	if value.Buffer {
		from := factory.NewPropertyAccessExpression(factory.NewIdentifier("Buffer"), nil, factory.NewIdentifier("from"), ast.NodeFlagsNone)
		return factory.NewCallExpression(from, nil, nil, arguments, ast.NodeFlagsNone)
	}
	return factory.NewNewExpression(factory.NewIdentifier("Uint8Array"), nil, arguments)
}
