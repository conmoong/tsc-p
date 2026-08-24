// Package bouncer provides the tsc-p declaration-gating emit plugin.
//
// This package is a tsc-p addition and is not part of upstream
// microsoft/TypeScript's tsc/ native compiler. It reads a `@bouncer` JSDoc tag on top-level
// function, class, variable, interface, type alias, and enum declarations
// and, per the tag, either:
//
//   - removes the declaration entirely from emit ("@bouncer remove"), or
//   - ensures it has a plain export modifier ("@bouncer export"), or
//   - strips its export modifier — and, in declaration output, omits it
//     entirely ("@bouncer no-export"), or
//   - makes it the module's default export ("@bouncer export default"), or
//   - exports it under a different name ("@bouncer export NewName"), or
//   - applies whichever of the above a named profile resolves to
//     ("@bouncer profile NAME"), where profiles are defined per project in
//     compilerOptions.plugins (see config.go).
//
// A declaration with no @bouncer tag, or one this plugin cannot parse
// (unrecognised directive text, a profile name with no matching config
// entry, or an export-default/export-as directive on a declaration with
// no single unambiguous name, e.g. "const a = 1, b = 2;"), is left
// completely unchanged.
//
// export-default and export-as never rename the declaration itself or
// touch references to it within the file: they strip any export/default
// modifier from the declaration and append a separate
// "export default <name>;" or "export { <name> as NewName };" statement
// after it. This plugin performs no whole-file consistency checking —
// two declarations both resolving to export-default in the same file, or
// an export-as name colliding with another export, produce invalid
// JavaScript, and it is the author's responsibility to avoid that; see
// the README for the wider version of this caveat (the same applies
// across files: removing or un-exporting a name another file imports).
//
// # Limitation: "export" and declaration output
//
// "@bouncer export" reliably adds the export modifier in JavaScript
// output. In declaration (.d.ts) output it has no effect for a
// declaration that was not already exported, in any file that has at
// least one other import or export (i.e. is a module in TypeScript's
// sense — true of essentially every real project file): TypeScript's own
// declaration transformer excludes non-exported top-level declarations
// from a module's declaration surface entirely, before this plugin's
// declaration hook ever runs, so there is no node left to add export to.
// (In a script file with no imports or exports at all, a top-level
// declaration is implicitly global/ambient and does appear regardless —
// but that is not the realistic case this limitation is about.) "remove"
// and "no-export" are unaffected by this, since they only ever act on
// declarations the transformer already decided to include (and
// "no-export" removes the declaration outright in declaration output
// rather than needing to add anything to it).
//
// The plugin participates in emit through the hook points defined in
// internal/tscp/hooks, activated and configured per project by a
// compilerOptions.plugins entry named PluginName (see config.go and
// internal/tscp/defaults); when inactive, emit behaves exactly as
// upstream.
package bouncer

import (
	"github.com/microsoft/TypeScript/tsc/internal/printer"
	"github.com/microsoft/TypeScript/tsc/internal/transformers"
	"github.com/microsoft/TypeScript/tsc/internal/tscp/hooks"
)

// plugin adapts Options to the tsc-p emit hook points.
type plugin struct {
	options *Options
}

var _ hooks.EmitPlugin = (*plugin)(nil)

// NewPlugin returns the bouncer emit plugin for the given options (nil =
// defaults: no profiles configured).
func NewPlugin(options *Options) hooks.EmitPlugin {
	if options == nil {
		options = defaultOptions()
	}
	return &plugin{options: options}
}

func (p *plugin) Name() string {
	return "bouncer"
}

func (p *plugin) ScriptTransformer(emitContext *printer.EmitContext, host printer.EmitHost) *transformers.Transformer {
	return NewTransformer(emitContext, p.options, EmitKindScript)
}

func (p *plugin) DeclarationTransformer(emitContext *printer.EmitContext, host printer.EmitHost) *transformers.Transformer {
	return NewTransformer(emitContext, p.options, EmitKindDeclaration)
}
