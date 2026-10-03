// Package tscp is the root of all tsc-p additions to this fork of
// microsoft/TypeScript's tsc/ native compiler. Everything specific to tsc-p lives under this
// directory; the only upstream files modified are the two hook points in
// tsc/internal/compiler (emitter.go and emitHost.go), each marked with a
// "tsc-p modification" comment.
//
// # Organisation
//
//	tsc/internal/tscp/
//	    hooks/          the compiled-in emit extension points (EmitPlugin),
//	                    plus shared tsconfig-plugins-array config helpers
//	    defaults/       tsconfig-driven plugin activation for the tsc-p binary
//	    pathrewrite/    plugin: module-specifier rewriting during emit
//	    bouncer/        plugin: @bouncer JSDoc-tag declaration removal/export
//	                    toggling/release-channel .d.ts trimming
//	    paris/          plugin: conditional compilation + value substitution
//	                    via @conmoong/paris runtime-library calls
//	    pure/           plugin: /*#__PURE__*/ annotations from @pure JSDoc tags
//	    graph/          plugin: module-graph fact gathering (files, exports,
//	                    resolved edges) plus cycle/phantomImport/devLeak/
//	                    unused/importRules checks; see its own package doc
//	                    and README.md's "@conmoong/graph" section for the
//	                    emitted .graph.json schema and the separate
//	                    @conmoong/graph-validate workspace-aggregation tool
//	    emit_test.go    integration tests through the full emit pipeline
//
// Concrete plugins are added as new sibling packages under tsc/internal/tscp;
// pathrewrite and bouncer are the reference examples. A plugin's
// config.go should read its compilerOptions.plugins entry through
// hooks.FindPluginEntry/ConfigGet/ConfigEach rather than re-parsing the
// raw config directly.
//
// # Plugin taxonomy
//
// tsc-p extensions fall into two families, and only the first needs the
// compiler hook points:
//
// Emit transform plugins implement hooks.EmitPlugin and rewrite the AST
// during emit. They run inside the compiler's transformer pipelines: the
// script transformer runs after import elision and before module lowering,
// and the declaration transformer runs after declaration generation and
// before printing. A transform plugin is a package implementing
// hooks.EmitPlugin, contributing a ScriptTransformer and/or a
// DeclarationTransformer (returning nil from the one it does not need);
// plugins needing capabilities beyond printer.EmitHost type-assert the
// emit host for narrow optional interfaces and opt out when unavailable.
// Transform plugins must use the node factory update helpers and
// EmitContext (SetOriginal, AssignCommentAndSourceMapRanges) so comments
// and source maps stay coherent, and must return nodes unchanged when they
// make no change so that upstream behaviour is preserved byte for byte.
//
// Program analysis plugins inspect a compiled program without changing
// emit. These need no compiler hooks at all: they consume the
// compiler.Program API (source files, resolved module references,
// diagnostics) from the CLI layer and report their own diagnostics. They
// should live under tsc/internal/tscp as sibling packages and must not modify
// the emit pipeline.
//
// # Rules
//
// This is deliberately not a generic third-party plugin system. There is
// no dynamic loading and no configuration-file plugin resolution; plugins
// are first-party Go packages compiled into the binary and wired up
// explicitly by the host through hooks.Provider. Plugins must be safe for
// concurrent use across files, must not use reflection, unsafe.Pointer or
// go:linkname, and must keep upstream output byte-identical whenever they
// decline to change anything.
package tscp
