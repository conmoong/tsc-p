# AGENTS.md

Guidance for AI agents working in this repository. Read this before making
changes, especially anything touching upstream files or the release
pipeline.

## What this repository is

tsc-p is an unofficial downstream fork of
[microsoft/TypeScript](https://github.com/microsoft/TypeScript)'s `tsc/`
native compiler (TypeScript 7; the former microsoft/typescript-go was
consolidated back into microsoft/TypeScript as `tsc/` and is now frozen).
It adds one thing on top of upstream: a compiled-in emit
hook that lets first-party "plugins" transform the AST during emit. Two
plugins ship, each opt-in per project via a ts-patch-style entry in
tsconfig's compilerOptions.plugins; with nothing configured — the
default — compiler behaviour is unchanged from upstream, byte for byte.
Not affiliated with or endorsed by Microsoft.

- **pathrewrite** (`@conmoong/path-rewrite`): rewrites tsconfig `paths`/
  package.json `imports` aliases and relative imports to real relative
  paths with destination-derived extensions (`.ts`→`.js`, `.mts`→`.mjs`,
  `.cts`→`.cjs`, `.json` kept).
- **bouncer** (`@conmoong/bouncer`): reads a `@bouncer` JSDoc tag on
  top-level declarations — `remove` (elide entirely), `export` (ensure
  exported), `no-export` (strip export in JS; omit entirely from `.d.ts`),
  `export default` / `export NewName` (append a separate export statement
  without renaming the declaration or its references), `release LEVEL`
  (`public`/`beta`/`alpha`/`internal`; with a tsconfig `release` channel
  configured, trims more-experimental declarations from `.d.ts` output
  only — JS is never affected, matching API Extractor's rollup semantics;
  no configured channel = no-op), or `profile NAME` (resolve via tsconfig
  `profiles`, which can itself resolve to any of the above including
  `{"release": "LEVEL"}`).
- **pure** (`@conmoong/pure`): a `@pure` JSDoc tag on a top-level variable
  statement adds `/*#__PURE__*/` before every call/`new` expression on its
  initialisers' module-evaluation spine (function/class bodies excluded),
  so bundlers can tree-shake the initialiser when unused —
  microsoft/TypeScript#13721, still open since 2017. An explicit marker,
  never a heuristic; untagged code and unconfigured projects are
  byte-identical to upstream.
- **paris** (`@conmoong/paris`): conditional compilation + value
  substitution as ordinary runtime-library calls
  (`ifDef`/`ifEq`/.../`value`), frozen at build time — true bodies become
  plain blocks, false branches vanish (inner conditions unevaluated),
  `value()` becomes a literal, the import is elided. Deliberately
  explicit-and-loud: misuse and bad config are BUILD FAILURES via the
  plugin-diagnostics seam (see below), and the published runtime package
  (a separate repository — see its own README/AGENTS.md there) throws
  un-compiled unless `CON_MOONG_PARIS_EN=BOUTEILLE` opts in. Every rule
  (block emit, strict ===, allow-list truthiness, RE2 errors, no
  return/var, undefined-variable errors) is a deliberate user decision;
  check that package's own docs before changing runtime semantics, and
  keep the Go-side plugin here (`tsc/internal/tscp/paris/`) and that
  package's rules in lockstep.
- **graph** (`@conmoong/graph`): gathers module-graph facts (files,
  exports, resolved edges — never modifies emit; byte-parity always
  holds) and, depending on `"emit"`, either writes a fact-only
  `.graph.json` next to the tsconfig (no rule evaluation in this mode —
  see the separate `@conmoong/graph-validate` package, its own
  repository, for combining facts across a `references` tree) or
  evaluates `"rules"` inline as ordinary build diagnostics: `cycle`,
  `phantomImport` (used but undeclared), `devLeak` (used but only a
  devDependency), `unused` (file or dependency never imported — requires
  the whole graph, so only evaluated once tsc-p has seen every file, and
  only ever deferred, never inline, when `"emit"` is set), and
  `importRules` (boundary/layering). Registered LAST among script
  transformers. The `.graph.json` schema is the contract between this
  plugin and `@conmoong/graph-validate` — check that package's README
  before changing it.

**Distribution beyond the native platform matrix**: `@conmoong/tsc-p-wasi`
(`tsc-p/packages/tsc-p-wasi/`) publishes a single universal `GOOS=wasip1 GOARCH=wasm`
build, executed in-process via Node's built-in `node:wasi` — no separate
WASI runtime needed. It is NOT in the root package's
`optionalDependencies` (an explicit, separate install); `main.js` falls
back to it automatically only on an `UnsupportedPlatformError` (a
distinguishable error class — never on any other `getExePath` failure,
which is a real installation problem, not something to silently paper
over). See `tsc-p/docs/BUILDING.md`'s "The WASI fallback" section before
touching this — it documents a non-obvious `PWD`-environment-variable fix
that's easy to accidentally regress (WASI has no real `getcwd()` syscall).

Full context: [README.md](README.md),
[tsc/internal/tscp/doc.go](tsc/internal/tscp/doc.go) (plugin architecture),
[tsc-p/docs/BUILDING.md](tsc-p/docs/BUILDING.md),
[tsc-p/docs/UPSTREAM-SYNC.md](tsc-p/docs/UPSTREAM-SYNC.md),
[tsc-p/docs/RELEASING.md](tsc-p/docs/RELEASING.md).

## The one rule that matters most

**This fork is minimal by construction, not minimal by cleanup.** Any
upstream file you have no reason to touch — `packages/` (including
`packages/vscode-typescript` and `packages/vscode-typescript-nightly`),
`tools/`, `Herebyfile.mjs` (beyond the one pinned block noted below), the
fourslash harness under `tsc/testdata/`, `.github/workflows/ci.yml`,
`codeql.yml` — leave completely alone. Never edit it, never delete it, even
if it looks unused or in your way. Touching a file you don't need turns a
silent future upstream merge into a conflict; not touching it means
upstream changes merge in with zero action from us.

The only upstream files this fork modifies, ever, are:

| File | What changed |
|---|---|
| `tsc/internal/compiler/emitter.go` | Two emit-plugin hook points (marked `tsc-p modification`) |
| `tsc/internal/compiler/emitHost.go` | Forwards plugins from the compiler host, applies the tsconfig-configured defaults, and exposes a resolution-cache lookup for plugins (marked `tsc-p modification`) |
| `tsc/internal/core/version.go` | Pins the reported compiler version to the stable release tsc-p tracks, instead of upstream's own dev/prerelease string |
| `package.json` | Adds `tscp:*` scripts (build/package/test/publish, under `tsc-p/scripts/`); every existing script is untouched |
| `.gitattributes` | Two `merge=ours` lines — `README.md` (the fork readme) and `Herebyfile.mjs` (see below) — so both always keep this fork's version across an upstream merge instead of conflicting |
| `Herebyfile.mjs` | Pins the native-preview release profile to tsc-p's tracked stable version instead of upstream's own prerelease/nightly profile |
| `README.md` | Replaced with the fork readme |

Everything else tsc-p adds lives under `tsc/internal/tscp/` (Go) and
`tsc-p/` (npm packaging, scripts, docs, CI). If a change to this fork
seems to require touching another upstream file beyond this table, stop
and reconsider the approach before proceeding — that is very likely the
wrong design, not a necessary exception. If it truly is necessary, say so
explicitly and explain why before making the change.

## Branch model — do not confuse these

| Branch | Purpose | Rules |
|---|---|---|
| `dev` | Default branch: upstream `main` + the tsc-p patch | All real work happens here. Ships `-edge` prereleases under npm's `next`. |
| `sync/upstream-main` | Head of the nightly upstream-sync PR (CI-merged upstream `main`) | **Disposable.** Force-pushed nightly. |
| `release-candidate` | Upstream's release lane + the tsc-p patch, regenerated by `portPatch.mjs` | **Disposable and derived.** Force-rebuilt on every port — anything committed here is destroyed. Fixes go in `dev`, or `tsc-p/patches/<lane>/`. |

Two lanes exist because upstream has two: its `main` is the next minor's
development line, and stable tags live only on a release branch that
periodically merges `main`. Those tags are **not** ancestors of `main`, so a
stable base can only be reached by porting the patch onto it, never by
merging. There is deliberately no pristine `main` mirror — the tooling reads
`upstream/*` remote-tracking refs directly, and a stale mirror is worse than
none.

Upstream is synced by **merging** into `dev`, never by rebasing — this is
deliberate so the `.gitattributes` `merge=ours` entries keep working and
history stays honest. Note that those entries need
`git config merge.ours.driver true` locally; GitHub's merge button has no such
config, which is why the nightly canary performs the merge in CI and opens a
fast-forward PR rather than asking GitHub to merge upstream directly.

Versioning is tied to the lane, and enforced both ways by
`npm run tscp:check:policy`: `upstream.tag: null` (tracking untagged upstream)
requires a prerelease version published under `next`; a real `upstream.tag`
forbids one. See
[tsc-p/docs/UPSTREAM-SYNC.md](tsc-p/docs/UPSTREAM-SYNC.md) before touching
sync machinery.

## tsc/internal/tscp: the plugin architecture

Everything tsc-p adds to the Go compiler lives under `tsc/internal/tscp/`:

```
tsc/internal/tscp/
    doc.go          architecture + rules (read this first)
    hooks/          EmitPlugin interface + Provider seam (compiler-side contract),
                    plus shared tsconfig-plugins-array config-reading helpers
    defaults/       tsconfig-driven plugin activation for the tsc-p binary
    pathrewrite/    plugin: module-specifier rewriting (opt-in via tsconfig plugins)
    bouncer/        plugin: @bouncer JSDoc-tag declaration removal/export
                    toggling/release-channel .d.ts trimming
    paris/          plugin: conditional compilation + value substitution
                    via @conmoong/paris runtime-library calls
    pure/           plugin: /*#__PURE__*/ annotations from @pure JSDoc tags
    graph/          plugin: module-graph fact gathering + cycle/phantomImport/
                    devLeak/unused/importRules checks (see README.md and
                    the separate @conmoong/graph-validate repository)
    emit_test.go    integration tests through the real emit pipeline
```

Concrete plugins are added as new sibling packages here as their own,
separate pieces of work; pathrewrite and bouncer are the reference
examples. Both plugins' config.go read compilerOptions.plugins through the
shared hooks.FindPluginEntry/ConfigGet/ConfigEach helpers — reuse those
rather than re-parsing the raw config, and extend them if a new need
arises rather than duplicating parsing logic in a new plugin.

This is deliberately **not** a generic third-party plugin system: no
dynamic loading, no config-file plugin references. Plugins are first-party
Go packages compiled into the binary and wired up explicitly through
`hooks.Provider`. If asked to add a new capability (e.g. an
argument-validation injector, a dependency-hygiene checker), read
`doc.go`'s plugin taxonomy first — it distinguishes *emit transform*
plugins (need the hook points, implement `EmitPlugin`) from *program
analysis* plugins (consume `compiler.Program`, need no compiler hooks at
all, must not touch emit).

Rules for any new transform plugin: use node factory update helpers and
`EmitContext` (`SetOriginal`, `AssignCommentAndSourceMapRanges`) — never
edit source text directly; must be safe for concurrent use across files
(no mutable global state); must return nodes unchanged when declining to
act, so identity behaviour stays byte-for-byte with upstream; no
reflection, `unsafe.Pointer`, or `go:linkname`.

### Non-obvious compiler behaviour any new plugin will hit

These cost real debugging time building pathrewrite and bouncer; a future
plugin touching JSDoc, exports, or declaration emit will hit them again.

- **JSDoc lookup requires the true original node.** `node.JSDoc(file)`
  checks `NodeFlagsHasJSDoc`, then looks up `file.jsdocCache[node]` — a
  map keyed by **node pointer**. Every transformer in the pipeline shares
  one `EmitContext`/`NodeFactory`, and its `onUpdate` hook automatically
  calls `SetOriginal` on every factory `Update*` call — so `Flags` (and
  thus `NodeFlagsHasJSDoc`) get copied onto a new synthesised node by an
  *earlier* transformer (type eraser, import elision, etc.), but the
  `jsdocCache` entry stays keyed to the original pointer. Reading JSDoc off
  a node your plugin receives will silently return nothing unless you
  resolve through `EmitContext.ParseNode(node)` back to the true original
  first (fall back to the node itself if `ParseNode` returns nil — see
  `bouncer/transformer.go`'s `apply()`).
- **A non-exported top-level declaration is invisible to the declaration
  transformer before any plugin hook runs.** In any file with at least one
  other `import`/`export` (i.e. a module, not a script), TypeScript's own
  declaration transformer excludes non-exported top-level declarations
  from the declaration AST entirely — there is no node left in `.d.ts` for
  a plugin's declaration hook to add an export to. Verified independently:
  a plain non-exported function with no plugin tag at all is equally
  absent. This is why bouncer's `export`/`export default`/`export NewName`
  reliably work in JavaScript but not in declarations for a previously
  non-exported name (documented in README's Bouncer section, along with
  the real mitigation: a manual `export type { Name };` anywhere in the
  file makes the transformer treat the name as exported from the start,
  independently of any plugin).
- **Not every module specifier the checker resolves is in
  `Program.resolvedModules`.** That cache is populated by the file loader
  walking real `import`/`export`/`require` statements in parsed source;
  specifiers the *checker* resolves for itself via its own
  `resolveExternalModule` calls — the automatic JSX runtime import
  (`jsxImportSource`) is the concrete case — never get an entry, because
  no explicit import statement exists for the file loader to walk in the
  first place. Both `GetResolvedModuleFromModuleSpecifier` (needs a usable
  parent chain) and the `GetResolvedModule` cache-by-text fallback
  (`pathrewrite/rewriter.go`'s `textModeResolver` tier) therefore always
  miss for it. The fix is a third tier calling `Program.ResolveModuleName`
  (already exposed on `emitHost`, upstream, unrelated to any tsc-p hook)
  for a **fresh** resolution instead of a cache lookup — real filesystem
  cost, so gate it behind the cheaper tiers failing first, as
  `pathrewrite/rewriter.go`'s `liveResolver` tier does. Any future plugin
  rewriting specifiers on synthesised nodes will hit the same gap for any
  other checker-only-resolved specifier.
- **Emit plugins can fail the build.** `hooks.FileDiagnosticsProvider` is
  the seam: a plugin records diagnostics per file (thread-safe, keyed by
  `tspath.Path`) during its transform, and the emitter drains them into
  the emit result after each file's transformers run (both pipelines) —
  additive hook lines in `emitter.go` only. Custom diagnostic messages
  cannot be constructed outside `tsc/internal/diagnostics` (unexported
  fields); tsc-p's live in `tsc/internal/diagnostics/tscp.go` — a **new
  file inside an upstream package**, which is always allowed (new files
  never conflict on upstream merges; editing upstream files is what the
  table above restricts). Codes use a 99xxxx range.
- **defaults.EmitPlugins is cached per (rawConfig, configDir)** when the
  raw value is comparable: the emitter asks for plugins several times per
  file, construction may do file I/O (paris), and stateful plugins must be
  shared across a compilation for their diagnostics to drain correctly.
- **One visitor callback can return multiple statements.**
  `factory.NewSyntaxList([]*ast.Node{a, b})` returned from a transform
  callback gets flattened automatically into the surrounding statement
  list (`internal/ast/visitor.go` handles `KindSyntaxList` specially) —
  the same mechanism upstream's own declaration transformer uses for
  class-expression-to-declaration lowering. This is how bouncer appends a
  trailing `export default name;` / `export { name as NewName };`
  statement after a declaration without renaming the declaration itself
  or rewriting references to it throughout the file.

## tsc-p: npm distribution layer

`tsc-p/manifest.json` is the single source of truth for what actually
varies release to release: tsc-p version, pinned upstream tag/commit, Go
version, the `platforms` array (one entry per supported OS/arch pair —
`nodejsPlatform`/`nodejsArch` for npm's `os`/`cpu` fields, `ghActionRunner`
for CI), and the `goTargets` array (one entry per Go build target, native
platforms plus a `wasi` entry — `goOs`/`goArch` for the Go build, a
`type: "native" | "wasm"` field so scripts can branch on target kind
generically instead of comparing `name` to a hardcoded string, and a
`platform` field linking a native target back to its `platforms` entry).
Scripts, the launcher, and CI all derive from these via `getManifest()` in
`tsc-p/scripts/lib.mjs`. If you're about to hard-code a version or a
platform pair anywhere, stop: read it from the manifest instead.

`type` is intentionally coarser than `name`: it exists for checks that
mean "is this a native, npm-auto-installable per-platform binary" (root
launcher `optionalDependencies`, `os`/`cpu` field validation), which
should hold for *any* wasm target — the WASI fallback today, and a future
`GOOS=js GOARCH=wasm` browser-playground target. It is **not** a stand-in
for "is this specifically the `wasi` package": the WASI-specific branches
in `package.mjs`/`verifyPackages.mjs`/`bintest.mjs` (copying
`tsc-p/packages/tsc-p-wasi`'s own `bin`/`lib` layout, running via
`lib/run.mjs`/`node:wasi`) stay keyed on `target.name === "wasi"`
literally, because a second wasm target would need entirely different
packaging (no npm CLI package, no `node:wasi` runner) — conflating the
two would silently mis-package it the moment it's added.

The package/binary name (`"tsc-p"`), npm scope (`"@conmoong"`), and the
repository URL are deliberately **not** in the manifest — they're
identical everywhere they appear (repo name, CLI name, GitHub org), so
they're hardcoded directly as literals: in `tsc-p/scripts/*.mjs` (e.g.
`package.mjs`'s `@conmoong/${goTarget.basename}` template literals and its
`repository.url` string), and in the committed package.json/README
templates under `tsc-p/packages/tsc-p/` and `tsc-p/packages/tsc-p-wasi/`.
There is no shared constant module for this — the duplication across
those literals is accepted, not a bug.

The launcher's own `package.json` template deliberately carries **no**
`optionalDependencies` — `package.mjs` reconstructs the full map at build
time from `manifest.goTargets` filtered to `type === "native"` (`wasi` is
never installed automatically). `getExePath.js` reads
`optionalDependencies` off whatever `package.json` it finds at runtime (or
an `overrides.package` object in tests) to know which platforms are
supported; there is no committed file for its tests to cross-check against
the manifest, by design — see `tsc-p/test/getExePath.test.mjs`'s
`fakePackage()` helper.

Build/test/package commands are documented in
[tsc-p/docs/BUILDING.md](tsc-p/docs/BUILDING.md); use `npm run
tscp:release:dry-run` to rehearse the entire pipeline locally before
believing anything works.

### Porting the patch to upstream's release lane

`npm run tscp:port -- --onto upstream/ts7-release` regenerates
`release-candidate` from scratch: upstream's release line plus the tsc-p
patch. The release lane is **derived, never maintained** — that is what stops
the two lanes from each needing their own upkeep.

`portPatch.mjs` enforces the minimal-fork rule mechanically: the modified
upstream files are checked against an explicit allowlist, and the port fails
if it ever changes. Touching an eleventh upstream file is therefore a
deliberate act with a CI failure attached, not something that can drift in.
Keep that list and the table above in sync.

Two escape hatches exist for genuine divergence between upstream's lanes,
both documented in `tsc-p/patches/ts7-release/README.md`:

- **Exclusions** (`DEV_LANE_ONLY_FILES`, `LANE_EXCLUDES`) — patches upstream
  owns on that lane. The `version.go` and `Herebyfile.mjs` pins only make
  sense against a development line; a release lane already reports a stable
  version.
- **Overlays** (`tsc-p/patches/<lane>/*.patch`) — for a change whose
  `main`-lane diff cannot apply because the surrounding upstream code
  differs. Note that *added* files can hit this too: they cannot conflict
  textually, but they can fail to compile against a different lane's API.

Prefer fixing things so no overlay is needed; every overlay is the same
change expressed twice, and both copies have to be kept honest.

## Sibling packages (separate repositories)

`@conmoong/paris`, `@conmoong/graph-validate`, and `@conmoong/teo` are
tsc-p-adjacent packages that live in their own repositories, not here —
each has its own README and AGENTS.md there. This repository has no code
dependency on any of them:

- **paris** and **graph-validate** are the JS-side runtime/tooling
  companions to the `paris` and `graph` Go plugins described above; the
  contract between each pair (the runtime gate behaviour, the
  `.graph.json` schema) is what couples them, not shared code. Check the
  relevant package's own docs before changing either side.
- **teo** is a plugin-aware TypeScript execution orchestrator (`teo
  file.ts`, plus `node --import @conmoong/teo/register` hooks) that runs
  `.ts` files through a real tsc-p binary so pathrewrite/bouncer apply at
  execution time. It depends on tsc-p only as an external binary
  (`TEO_TSC` env, with a fallback to a plain `typescript` devDependency
  when tsc-p isn't installed) — never as a monorepo import.

## Verification standard

Do not declare a change complete because a command exited zero. This
project's own conventions (and its test suite) go out of their way to
check real output: byte-parity of emit against a pristine upstream binary,
clean-room npm installs into directories containing spaces, executing
emitted JavaScript and asserting on stdout, inspecting actual tarball
contents rather than trusting `npm pack`'s logs. Match that standard for
new work — if you change anything near the emit pipeline or the packaging
scripts, re-run `npm run tscp:release:dry-run` and actually look at what it
built, not just whether it exited 0.

## Commit conventions

- Conventional Commits format (`feat(scope): ...`, `fix: ...`, etc.).
- No AI-attribution trailer (no `Co-Authored-By: Claude ...` or similar).
- Only create commits when the user asks for one; don't commit
  proactively mid-task.
