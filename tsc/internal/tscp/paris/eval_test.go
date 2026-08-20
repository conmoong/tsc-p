package paris

import (
	"strings"
	"testing"

	"gotest.tools/v3/assert"
)

func evalOK(t *testing.T, pred predicate, value *Value, arg *comparand) bool {
	t.Helper()
	result, errText := evaluate(pred, "V", value, arg, nil)
	assert.Equal(t, errText, "")
	return result
}

func evalErr(t *testing.T, pred predicate, value *Value, arg *comparand) string {
	t.Helper()
	_, errText := evaluate(pred, "V", value, arg, nil)
	assert.Assert(t, errText != "", "expected an error")
	return errText
}

func str(s string) *Value         { return &Value{Kind: KindString, Str: s} }
func num(n float64) *Value        { return &Value{Kind: KindNumber, Num: n} }
func boolean(b bool) *Value       { return &Value{Kind: KindBool, Bool: b} }
func argStr(s string) *comparand  { return &comparand{kind: KindString, str: s} }
func argNum(n float64) *comparand { return &comparand{kind: KindNumber, num: n} }
func argBool(b bool) *comparand   { return &comparand{kind: KindBool, b: b} }

func TestDefinedness(t *testing.T) {
	t.Parallel()
	assert.Assert(t, !evalOK(t, predIfDef, nil, nil))
	assert.Assert(t, evalOK(t, predIfNotDef, nil, nil))
	assert.Assert(t, evalOK(t, predIfDef, str(""), nil), "empty string is still defined")
	assert.Assert(t, strings.Contains(evalErr(t, predIfEq, nil, argStr("x")), "not defined"))
	assert.Assert(t, strings.Contains(evalErr(t, predIfTruthy, nil, nil), "not defined"))
}

func TestStrictEquality(t *testing.T) {
	t.Parallel()
	assert.Assert(t, evalOK(t, predIfEq, str("123"), argStr("123")))
	assert.Assert(t, !evalOK(t, predIfEq, num(123), argStr("123")), "number 123 !== string \"123\"")
	assert.Assert(t, !evalOK(t, predIfEq, str("123"), argNum(123)))
	assert.Assert(t, evalOK(t, predIfEq, num(123), argNum(123)))
	assert.Assert(t, evalOK(t, predIfEq, boolean(true), argBool(true)))
	assert.Assert(t, evalOK(t, predIfNotEq, num(123), argStr("123")))
	assert.Assert(t, strings.Contains(evalErr(t, predIfEq, &Value{Kind: KindJSON}, argStr("x")), "not comparable"))
}

func TestOrdering(t *testing.T) {
	t.Parallel()
	assert.Assert(t, evalOK(t, predIfGt, num(10), argNum(5)))
	assert.Assert(t, !evalOK(t, predIfGt, num(5), argNum(5)))
	assert.Assert(t, evalOK(t, predIfGte, num(5), argNum(5)))
	assert.Assert(t, evalOK(t, predIfLt, num(4), argNum(5)))
	assert.Assert(t, evalOK(t, predIfLte, num(5), argNum(5)))
	assert.Assert(t, evalOK(t, predIfGt, str(" 10 "), argNum(5)), "numeric strings compare numerically")
	assert.Assert(t, strings.Contains(evalErr(t, predIfGt, str("abc"), argNum(5)), "not a number"))
	assert.Assert(t, strings.Contains(evalErr(t, predIfGt, boolean(true), argNum(5)), "not a number"))
}

func TestStrictBooleans(t *testing.T) {
	t.Parallel()
	assert.Assert(t, evalOK(t, predIfTrue, boolean(true), nil))
	assert.Assert(t, !evalOK(t, predIfTrue, boolean(false), nil))
	assert.Assert(t, evalOK(t, predIfFalse, boolean(false), nil))
	assert.Assert(t, strings.Contains(evalErr(t, predIfTrue, str("true"), nil), "strictly boolean"))
}

func TestTruthyAllowList(t *testing.T) {
	t.Parallel()
	for _, truthy := range []*Value{boolean(true), num(1), num(-2), str("1"), str("42.5"), str("true"), str("TRUE"), str("on"), str("Yes")} {
		assert.Assert(t, evalOK(t, predIfTruthy, truthy, nil), "%v should be truthy", truthy)
	}
	for _, falsy := range []*Value{boolean(false), num(0), str("0"), str(""), str("false"), str("off"), str("no"), str("banana"), str("enabled")} {
		assert.Assert(t, !evalOK(t, predIfTruthy, falsy, nil), "%v should be falsy (allow-list, not JS truthiness)", falsy)
	}
	assert.Assert(t, evalOK(t, predIfNotTruthy, str("banana"), nil))
}

func TestRegexMatching(t *testing.T) {
	t.Parallel()
	pattern, errText := compileJSRegex("^pro", "i")
	assert.Equal(t, errText, "")
	result, errText := evaluate(predIfMatch, "V", str("Production"), nil, pattern)
	assert.Equal(t, errText, "")
	assert.Assert(t, result)
	result, errText = evaluate(predIfNotMatch, "V", str("dev"), nil, pattern)
	assert.Equal(t, errText, "")
	assert.Assert(t, result)
	_, errText = evaluate(predIfMatch, "V", num(1), nil, pattern)
	assert.Assert(t, strings.Contains(errText, "not a string"))

	_, errText = compileJSRegex("(?=lookahead)", "")
	assert.Assert(t, strings.Contains(errText, "RE2"), "JS-only regex syntax must be a loud error: %s", errText)
	_, errText = compileJSRegex("x", "q")
	assert.Assert(t, strings.Contains(errText, "unsupported regex flag"))
}
