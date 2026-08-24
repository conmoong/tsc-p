package pathrewrite

import (
	"strings"

	"github.com/microsoft/TypeScript/tsc/internal/ast"
	"github.com/microsoft/TypeScript/tsc/internal/core"
	"github.com/microsoft/TypeScript/tsc/internal/module"
	"github.com/microsoft/TypeScript/tsc/internal/printer"
	"github.com/microsoft/TypeScript/tsc/internal/tspath"
)

// moduleResolver is the narrow, optional capability the host rewriter needs
// beyond printer.EmitHost. The compiler's emit host implements it; test
// hosts that do not are simply left unrewritten.
type moduleResolver interface {
	GetResolvedModuleFromModuleSpecifier(file ast.HasFileName, moduleSpecifier *ast.StringLiteralLike) *module.ResolvedModule
}

// textModeResolver is a further optional capability used to resolve
// specifiers on synthesised nodes (for example the import types the
// declaration transformer creates for inferred return types), whose parent
// chains cannot support the per-usage resolution-mode computation. The
// lookup falls back to the file's default resolution mode; a cache miss
// simply leaves the specifier unrewritten.
type textModeResolver interface {
	GetResolvedModule(file ast.HasFileName, moduleReference string, mode core.ResolutionMode) *module.ResolvedModule
	GetDefaultResolutionModeForFile(file ast.HasFileName) core.ResolutionMode
}

// liveResolver is a last-resort optional capability for specifiers that
// were never walked by the file loader at all — notably the automatic JSX
// runtime import (e.g. "react/jsx-runtime", or a relative path from
// jsxImportSource) that the checker resolves for itself via its own
// resolveExternalModule call, never recording an entry in the program's
// resolvedModules cache the way a real import/export statement would.
// Both cache-based tiers above therefore always miss for it; this tier
// performs a fresh resolution instead of a cache lookup, at real
// filesystem cost, so it only runs once the cheaper tiers have failed.
type liveResolver interface {
	ResolveModuleName(moduleName string, containingFile string, resolutionMode core.ResolutionMode) *module.ResolvedModule
}

// hostRewriter rewrites specifiers using the compiler's own resolution
// results. It holds only immutable references and is safe for concurrent
// use across files.
type hostRewriter struct {
	resolver                  moduleResolver
	options                   *Options
	useCaseSensitiveFileNames bool
	currentDirectory          string
}

var _ ModuleSpecifierRewriter = (*hostRewriter)(nil)

// NewHostRewriter returns a rewriter backed by the emit host's module
// resolution, or nil when the host cannot supply it. A nil options uses
// the defaults (everything rewritten, with extensions).
func NewHostRewriter(host printer.EmitHost, options *Options) ModuleSpecifierRewriter {
	resolver, ok := host.(moduleResolver)
	if !ok {
		return nil
	}
	if options == nil {
		options = defaultOptions()
	}
	return &hostRewriter{
		resolver:                  resolver,
		options:                   options,
		useCaseSensitiveFileNames: host.UseCaseSensitiveFileNames(),
		currentDirectory:          host.GetCurrentDirectory(),
	}
}

// RewriteModuleSpecifier implements ModuleSpecifierRewriter.
func (r *hostRewriter) RewriteModuleSpecifier(context RewriteContext, specifier string) (string, bool) {
	if context.SourceFile == nil || context.SpecifierNode == nil {
		return specifier, false
	}

	rewrite, withExtension := r.options.decide(specifier)
	if !rewrite {
		return specifier, false
	}

	resolved := resolveSpecifier(r.resolver, context.SourceFile, context.SpecifierNode, specifier)
	if !resolved.IsResolved() {
		return specifier, false
	}
	// Never rewrite package imports: a specifier resolving into
	// node_modules must keep its published form.
	if resolved.IsExternalLibraryImport || strings.Contains(resolved.ResolvedFileName, "/node_modules/") {
		return specifier, false
	}

	outputExt, ok := outputExtension(resolved.Extension)
	if !ok {
		return specifier, false
	}
	// Extensionless rewrites drop the mapped extension — except for .json,
	// which TypeScript itself requires and which is broken at runtime
	// without it.
	if !withExtension && outputExt != tspath.ExtensionJson {
		outputExt = ""
	}
	target := strings.TrimSuffix(resolved.ResolvedFileName, resolved.Extension) + outputExt

	relative := tspath.GetRelativePathFromFile(context.SourceFile.FileName(), target, tspath.ComparePathsOptions{
		UseCaseSensitiveFileNames: r.useCaseSensitiveFileNames,
		CurrentDirectory:          r.currentDirectory,
	})
	relative = tspath.EnsurePathIsNonModuleName(relative)
	if relative == specifier {
		return specifier, false
	}
	return relative, true
}

// outputExtension maps a resolved file's extension to the extension its
// emitted counterpart will have. The mapping depends only on the resolved
// file, never on the emitting module format: a .ts file emits foo.js
// whether the output is ESM or CommonJS, .mts always emits .mjs, and .cts
// always emits .cjs, so the same specifier is correct in both pipelines.
// Extensions with no emitted mapping (notably .json, which must survive
// as-is) map to themselves; unknown extensions report ok == false and the
// specifier is left alone.
func outputExtension(resolvedExtension string) (ext string, ok bool) {
	switch resolvedExtension {
	case tspath.ExtensionTs, tspath.ExtensionTsx, tspath.ExtensionDts:
		return tspath.ExtensionJs, true
	case tspath.ExtensionMts, tspath.ExtensionDmts:
		return tspath.ExtensionMjs, true
	case tspath.ExtensionCts, tspath.ExtensionDcts:
		return tspath.ExtensionCjs, true
	case tspath.ExtensionJs, tspath.ExtensionJsx:
		// Already-JavaScript sources (allowJs) emit under .js.
		return tspath.ExtensionJs, true
	case tspath.ExtensionMjs:
		return tspath.ExtensionMjs, true
	case tspath.ExtensionCjs:
		return tspath.ExtensionCjs, true
	case tspath.ExtensionJson:
		// JSON is not transformed; the specifier must keep .json.
		return tspath.ExtensionJson, true
	default:
		return "", false
	}
}

// ResolveSpecifier resolves a module specifier using the same tiered
// strategy this plugin's own rewriting relies on: the compiler's per-usage
// resolution-cache lookup when specifierNode has a usable parent chain, a
// text-keyed cache lookup probing plausible resolution modes when it
// doesn't (or the first tier misses), and — as a last resort — a fresh
// resolution for specifiers the file loader never walked at all (the
// automatic JSX runtime import from a relative jsxImportSource is the
// concrete case this tier was added for; see rewriter.go's history).
// Exported so other tsc-p plugins needing robust specifier resolution (for
// example @conmoong/graph) reuse this exact, previously-buggy-until-fixed
// mechanism rather than risking a second, diverging copy. Returns nil (not
// resolved) if host does not support the resolution capabilities this
// requires — real hosts always do; only degenerate test hosts might not.
func ResolveSpecifier(host printer.EmitHost, sourceFile *ast.SourceFile, specifierNode *ast.Node, specifierText string) *module.ResolvedModule {
	resolver, ok := host.(moduleResolver)
	if !ok {
		return nil
	}
	return resolveSpecifier(resolver, sourceFile, specifierNode, specifierText)
}

func resolveSpecifier(resolver moduleResolver, sourceFile *ast.SourceFile, specifierNode *ast.Node, specifierText string) *module.ResolvedModule {
	var resolved *module.ResolvedModule
	if hasUsableParentChain(specifierNode) {
		resolved = resolver.GetResolvedModuleFromModuleSpecifier(sourceFile, specifierNode)
	}
	if !resolved.IsResolved() {
		if textMode, ok := resolver.(textModeResolver); ok {
			// Synthesised node with an incomplete parent chain (or a usable
			// chain whose cache lookup missed): the per-usage mode
			// computation would dereference nil parents in the former case,
			// so fall back to a text lookup against the program's resolution
			// cache. The cache is keyed by (text, mode) and the mode this
			// usage was stored under is unrecoverable here, so probe the
			// possible modes in order of likelihood; a hit is always a
			// resolution the program genuinely performed for this text in
			// this file.
			resolved = resolveByProbingModes(textMode.GetDefaultResolutionModeForFile(sourceFile), func(mode core.ResolutionMode) *module.ResolvedModule {
				return textMode.GetResolvedModule(sourceFile, specifierText, mode)
			})
		}
	}
	if !resolved.IsResolved() {
		if live, ok := resolver.(liveResolver); ok {
			// Last resort: a specifier the file loader never walked at all,
			// so no cache lookup can ever find it. Perform a fresh
			// resolution instead, at real filesystem cost — bounded, since
			// both cheaper tiers already missed.
			defaultMode := core.ResolutionModeNone
			if textMode, ok := resolver.(textModeResolver); ok {
				defaultMode = textMode.GetDefaultResolutionModeForFile(sourceFile)
			}
			resolved = resolveByProbingModes(defaultMode, func(mode core.ResolutionMode) *module.ResolvedModule {
				return live.ResolveModuleName(specifierText, sourceFile.FileName(), mode)
			})
		}
	}
	return resolved
}

// resolveByProbingModes tries resolve, in order, against defaultMode and
// then the other resolution modes a usage could plausibly have been
// recorded or resolved under, stopping at the first resolved result. Used
// by both cache-lookup and live-resolution tiers: in each case, the exact
// mode a specifier's usage corresponds to cannot be recovered from a
// synthesised node with no usable parent chain, so likely modes are tried
// in order instead.
func resolveByProbingModes(defaultMode core.ResolutionMode, resolve func(mode core.ResolutionMode) *module.ResolvedModule) *module.ResolvedModule {
	modes := [...]core.ResolutionMode{defaultMode, core.ResolutionModeNone, core.ResolutionModeESM, core.ResolutionModeCommonJS}
	seen := map[core.ResolutionMode]bool{}
	for _, mode := range modes {
		if seen[mode] {
			continue
		}
		seen[mode] = true
		if resolved := resolve(mode); resolved.IsResolved() {
			return resolved
		}
	}
	return nil
}

// hasUsableParentChain reports whether the specifier node's parent chain is
// complete enough for the compiler's resolution-mode computation (see
// getModeForUsageLocation and getEmitSyntaxForUsageLocationWorker in
// internal/compiler/fileloader.go), which dereferences Parent — and for
// some shapes Parent.Parent — without nil checks.
func hasUsableParentChain(node *ast.Node) bool {
	parent := node.Parent
	if parent == nil {
		return false
	}
	switch {
	case ast.IsLiteralTypeNode(parent), // import type: needs the ImportTypeNode above
		ast.IsExternalModuleReference(parent), // import-equals: needs the declaration above
		ast.IsParenthesizedExpression(parent): // walked through when detecting import()
		return parent.Parent != nil
	}
	return true
}
