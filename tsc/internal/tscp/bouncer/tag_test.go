package bouncer

import (
	"testing"

	"github.com/microsoft/TypeScript/tsc/internal/testutil/parsetestutil"
	"gotest.tools/v3/assert"
)

func TestParseDirective(t *testing.T) {
	t.Parallel()

	options := &Options{Profiles: map[string]Action{
		"ABC": {Kind: ActionRemove},
		"XYZ": {Kind: ActionNoExport},
	}}

	cases := []struct {
		text   string
		action Action
		ok     bool
	}{
		{"remove", Action{Kind: ActionRemove}, true},
		{"export", Action{Kind: ActionExport}, true},
		{"export default", Action{Kind: ActionExportDefault}, true},
		{"export NewName", Action{Kind: ActionExportAs, Name: "NewName"}, true},
		{"no-export", Action{Kind: ActionNoExport}, true},
		{"release public", Action{Kind: ActionRelease, Name: "public"}, true},
		{"release beta", Action{Kind: ActionRelease, Name: "beta"}, true},
		{"release alpha", Action{Kind: ActionRelease, Name: "alpha"}, true},
		{"release internal", Action{Kind: ActionRelease, Name: "internal"}, true},
		{"release bogus-channel", Action{}, false},
		{"release", Action{}, false},
		{"profile ABC", Action{Kind: ActionRemove}, true},
		{"profile XYZ", Action{Kind: ActionNoExport}, true},
		{"profile UNKNOWN", Action{}, false},
		{"", Action{}, false},
		{"remove extra", Action{}, false},
		{"no-export extra", Action{}, false},
		{"export default extra", Action{}, false},
		{"profile", Action{}, false},
		{"bogus", Action{}, false},
	}
	for _, c := range cases {
		action, ok := parseDirective(c.text, options)
		assert.Equal(t, c.ok, ok, "ok for %q", c.text)
		assert.Equal(t, c.action, action, "action for %q", c.text)
	}
}

func TestActionForNodeNoTag(t *testing.T) {
	t.Parallel()

	file := parsetestutil.ParseTypeScript("export function mock() {}", false /*jsx*/)
	parsetestutil.CheckDiagnostics(t, file)
	fn := file.Statements.Nodes[0]

	assert.Equal(t, Action{Kind: ActionNone}, actionForNode(fn, defaultOptions()))
}

func TestActionForNodeDirectives(t *testing.T) {
	t.Parallel()

	cases := []struct {
		source string
		action Action
	}{
		{"/**\n * @bouncer remove\n */\nexport function mock() {}", Action{Kind: ActionRemove}},
		{"/**\n * @bouncer export\n */\nfunction mock() {}", Action{Kind: ActionExport}},
		{"/**\n * @bouncer no-export\n */\nexport function mock() {}", Action{Kind: ActionNoExport}},
		{"/**\n * @bouncer export default\n */\nfunction mock() {}", Action{Kind: ActionExportDefault}},
		{"/**\n * @bouncer export Renamed\n */\nfunction mock() {}", Action{Kind: ActionExportAs, Name: "Renamed"}},
		{"/**\n * @bouncer release beta\n */\nexport function mock() {}", Action{Kind: ActionRelease, Name: "beta"}},
	}
	for _, c := range cases {
		file := parsetestutil.ParseTypeScript(c.source, false /*jsx*/)
		parsetestutil.CheckDiagnostics(t, file)
		assert.Equal(t, c.action, actionForNode(file.Statements.Nodes[0], defaultOptions()), "source: %s", c.source)
	}
}

func TestActionForNodeProfile(t *testing.T) {
	t.Parallel()

	source := `/**
 * @bouncer profile ABC
 */
export function mock() {}`
	file := parsetestutil.ParseTypeScript(source, false /*jsx*/)
	parsetestutil.CheckDiagnostics(t, file)
	fn := file.Statements.Nodes[0]

	options := &Options{Profiles: map[string]Action{"ABC": {Kind: ActionNoExport}}}
	assert.Equal(t, Action{Kind: ActionNoExport}, actionForNode(fn, options))

	// An unconfigured profile name is a safe no-op, not an error.
	assert.Equal(t, Action{Kind: ActionNone}, actionForNode(fn, defaultOptions()))
}

func TestActionForNodeLastTagWins(t *testing.T) {
	t.Parallel()

	source := `/**
 * @bouncer no-export
 * @bouncer remove
 */
export function mock() {}`
	file := parsetestutil.ParseTypeScript(source, false /*jsx*/)
	parsetestutil.CheckDiagnostics(t, file)
	fn := file.Statements.Nodes[0]

	assert.Equal(t, Action{Kind: ActionRemove}, actionForNode(fn, defaultOptions()))
}

func TestActionForNodeUnrecognisedTagDoesNotClearEarlierOne(t *testing.T) {
	t.Parallel()

	// A later @bouncer tag this plugin cannot parse must not erase an
	// earlier valid one on the same node.
	source := `/**
 * @bouncer remove
 * @bouncer nonsense
 */
export function mock() {}`
	file := parsetestutil.ParseTypeScript(source, false /*jsx*/)
	parsetestutil.CheckDiagnostics(t, file)
	fn := file.Statements.Nodes[0]

	assert.Equal(t, Action{Kind: ActionRemove}, actionForNode(fn, defaultOptions()))
}

func TestActionForNodeOtherJSDocTagsIgnored(t *testing.T) {
	t.Parallel()

	source := `/**
 * @param x something
 * @returns nothing
 */
export function mock(x: number) {}`
	file := parsetestutil.ParseTypeScript(source, false /*jsx*/)
	parsetestutil.CheckDiagnostics(t, file)
	fn := file.Statements.Nodes[0]

	assert.Equal(t, Action{Kind: ActionNone}, actionForNode(fn, defaultOptions()))
}
