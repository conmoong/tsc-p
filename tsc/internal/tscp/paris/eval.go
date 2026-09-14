package paris

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// predicate identifies one of the plugin's condition functions.
type predicate int

const (
	predNone predicate = iota
	predIfDef
	predIfNotDef
	predIfEq
	predIfNotEq
	predIfMatch
	predIfNotMatch
	predIfGt
	predIfGte
	predIfLt
	predIfLte
	predIfTrue
	predIfFalse
	predIfTruthy
	predIfNotTruthy
	predValue // paris.value(name): substitution, not a condition
)

var predicateNames = map[string]predicate{
	"ifDef":       predIfDef,
	"ifNotDef":    predIfNotDef,
	"ifEq":        predIfEq,
	"ifNotEq":     predIfNotEq,
	"ifMatch":     predIfMatch,
	"ifNotMatch":  predIfNotMatch,
	"ifGt":        predIfGt,
	"ifGte":       predIfGte,
	"ifLt":        predIfLt,
	"ifLte":       predIfLte,
	"ifTrue":      predIfTrue,
	"ifFalse":     predIfFalse,
	"ifTruthy":    predIfTruthy,
	"ifNotTruthy": predIfNotTruthy,
	"value":       predValue,
}

// comparand is a literal second argument to ifEq/ifNotEq (string, number,
// or boolean) or the numeric bound of the ordering predicates.
type comparand struct {
	kind ValueKind // KindString, KindNumber, or KindBool
	str  string
	num  float64
	b    bool
}

// argCount returns how many arguments (including the callback) a predicate
// takes; value() takes one and no callback.
func (p predicate) argCount() int {
	switch p {
	case predIfDef, predIfNotDef, predIfTrue, predIfFalse, predIfTruthy, predIfNotTruthy:
		return 2
	case predValue:
		return 1
	default:
		return 3
	}
}

func (p predicate) name() string {
	for name, pred := range predicateNames {
		if pred == p {
			return name
		}
	}
	return "?"
}

// evaluate decides a condition at build time. value is nil when the
// variable is not defined. Only ifDef/ifNotDef tolerate an undefined
// variable; every other predicate reports an error (guard with nesting).
// A non-empty error string is a build-failing diagnostic.
func evaluate(pred predicate, varName string, value *Value, arg *comparand, pattern *regexp.Regexp) (bool, string) {
	switch pred {
	case predIfDef:
		return value != nil, ""
	case predIfNotDef:
		return value == nil, ""
	}
	if value == nil {
		return false, fmt.Sprintf("%s(%q): variable is not defined; guard with ifDef/ifNotDef nesting or define it", pred.name(), varName)
	}
	switch pred {
	case predIfEq, predIfNotEq:
		equal, errText := strictEquals(varName, value, arg)
		if errText != "" {
			return false, errText
		}
		return equal == (pred == predIfEq), ""
	case predIfGt, predIfGte, predIfLt, predIfLte:
		number, errText := asNumber(pred, varName, value)
		if errText != "" {
			return false, errText
		}
		switch pred {
		case predIfGt:
			return number > arg.num, ""
		case predIfGte:
			return number >= arg.num, ""
		case predIfLt:
			return number < arg.num, ""
		default:
			return number <= arg.num, ""
		}
	case predIfTrue, predIfFalse:
		if value.Kind != KindBool {
			return false, fmt.Sprintf("%s(%q): value is not a boolean (ifTrue/ifFalse are strictly boolean; values from env/file sources are strings)", pred.name(), varName)
		}
		return value.Bool == (pred == predIfTrue), ""
	case predIfTruthy, predIfNotTruthy:
		truthy, errText := isTruthy(pred, varName, value)
		if errText != "" {
			return false, errText
		}
		return truthy == (pred == predIfTruthy), ""
	case predIfMatch, predIfNotMatch:
		if value.Kind != KindString {
			return false, fmt.Sprintf("%s(%q): value is not a string", pred.name(), varName)
		}
		return pattern.MatchString(value.Str) == (pred == predIfMatch), ""
	}
	return false, fmt.Sprintf("internal: unhandled predicate %s", pred.name())
}

// strictEquals implements the === ruling: types must match; a literal of a
// different type than the value is simply not equal. JSON and byte values
// are not comparable.
func strictEquals(varName string, value *Value, arg *comparand) (bool, string) {
	switch value.Kind {
	case KindString:
		return arg.kind == KindString && value.Str == arg.str, ""
	case KindNumber:
		return arg.kind == KindNumber && value.Num == arg.num, ""
	case KindBool:
		return arg.kind == KindBool && value.Bool == arg.b, ""
	default:
		return false, fmt.Sprintf("ifEq/ifNotEq(%q): value is not comparable (JSON and binary values have no === semantics)", varName)
	}
}

func asNumber(pred predicate, varName string, value *Value) (float64, string) {
	switch value.Kind {
	case KindNumber:
		return value.Num, ""
	case KindString:
		number, err := strconv.ParseFloat(strings.TrimSpace(value.Str), 64)
		if err != nil {
			return 0, fmt.Sprintf("%s(%q): value %q is not a number", pred.name(), varName, value.Str)
		}
		return number, ""
	default:
		return 0, fmt.Sprintf("%s(%q): value is not a number", pred.name(), varName)
	}
}

// isTruthy implements the allow-list truthiness ruling (not JavaScript
// truthiness): booleans are themselves, numbers are non-zero, and strings
// are truthy only when they parse as a non-zero number or are one of
// "true", "on", "yes" (case-insensitive). Everything else is falsy;
// non-scalar values are errors.
func isTruthy(pred predicate, varName string, value *Value) (bool, string) {
	switch value.Kind {
	case KindBool:
		return value.Bool, ""
	case KindNumber:
		return value.Num != 0, ""
	case KindString:
		text := strings.TrimSpace(value.Str)
		if number, err := strconv.ParseFloat(text, 64); err == nil {
			return number != 0, ""
		}
		switch strings.ToLower(text) {
		case "true", "on", "yes":
			return true, ""
		}
		return false, ""
	default:
		return false, fmt.Sprintf("%s(%q): value is not a boolean, number, or string", pred.name(), varName)
	}
}

// compileJSRegex converts a JavaScript regex literal's pattern and flags to
// a Go RE2 regexp. The i, m, and s flags map to inline flags; u and v are
// meaningless under RE2 (which is always Unicode-aware) and g, d, y do not
// affect matching. JS-only syntax (lookahead, backreferences) fails RE2
// compilation and becomes a build error — never a silent mis-evaluation.
func compileJSRegex(pattern string, flags string) (*regexp.Regexp, string) {
	var inline strings.Builder
	for _, flag := range flags {
		switch flag {
		case 'i', 'm', 's':
			inline.WriteRune(flag)
		case 'g', 'u', 'v', 'd', 'y':
			// No effect on a boolean match test.
		default:
			return nil, fmt.Sprintf("unsupported regex flag %q", string(flag))
		}
	}
	source := pattern
	if inline.Len() > 0 {
		source = "(?" + inline.String() + ")" + pattern
	}
	compiled, err := regexp.Compile(source)
	if err != nil {
		return nil, fmt.Sprintf("regex is not supported by the build-time engine (RE2): %v", err)
	}
	return compiled, ""
}
