package bouncer

import (
	"strings"

	"github.com/microsoft/TypeScript/tsc/internal/ast"
	"github.com/microsoft/TypeScript/tsc/internal/scanner"
)

const tagName = "bouncer"

// actionForNode inspects node's JSDoc for @bouncer tags and returns the
// action they specify, resolving "profile NAME" against options. When a
// node carries more than one @bouncer tag, the last one (in source order)
// wins. A node with no @bouncer tag, or whose tags this plugin cannot
// parse, resolves to ActionNone.
func actionForNode(node *ast.Node, options *Options) Action {
	action := Action{Kind: ActionNone}
	for _, jsdoc := range node.JSDoc(nil) {
		tags := jsdoc.AsJSDoc().Tags
		if tags == nil {
			continue
		}
		for _, tag := range tags.Nodes {
			if !ast.IsJSDocUnknownTag(tag) {
				continue
			}
			unknown := tag.AsJSDocUnknownTag()
			if unknown.TagName == nil || unknown.TagName.Text() != tagName {
				continue
			}
			if parsed, ok := parseDirective(scanner.GetTextOfJSDocComment(unknown.Comment), options); ok {
				action = parsed
			}
		}
	}
	return action
}

// parseDirective parses the text following "@bouncer": "remove",
// "export", "export default", "export NewName", "no-export",
// "release LEVEL" (LEVEL one of "public", "beta", "alpha", "internal"),
// or "profile NAME". Anything else is reported as not-ok so the caller can
// leave the current action unchanged rather than resetting it to
// ActionNone — one unrecognised tag should not undo an earlier valid one
// on the same node.
func parseDirective(text string, options *Options) (Action, bool) {
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return Action{}, false
	}
	switch fields[0] {
	case "remove":
		if len(fields) == 1 {
			return Action{Kind: ActionRemove}, true
		}
	case "no-export":
		if len(fields) == 1 {
			return Action{Kind: ActionNoExport}, true
		}
	case "export":
		switch len(fields) {
		case 1:
			return Action{Kind: ActionExport}, true
		case 2:
			if fields[1] == "default" {
				return Action{Kind: ActionExportDefault}, true
			}
			return Action{Kind: ActionExportAs, Name: fields[1]}, true
		}
	case "release":
		if len(fields) == 2 {
			if _, ok := releaseRank[fields[1]]; ok {
				return Action{Kind: ActionRelease, Name: fields[1]}, true
			}
		}
	case "profile":
		if len(fields) == 2 {
			if action, ok := options.Profiles[fields[1]]; ok {
				return action, true
			}
			// Unknown profile name: not-ok, so this tag contributes
			// nothing (safe no-op), matching the unknown-profile policy.
		}
	}
	return Action{}, false
}
