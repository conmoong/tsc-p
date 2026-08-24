# tsc-p

> **tsc-p is an unofficial downstream fork of
> [microsoft/typescript-go](https://github.com/microsoft/typescript-go).
> It is not affiliated with, maintained by, or endorsed by Microsoft.**

tsc-p is a native TypeScript compiler based on TypeScript 7 (TypeScript-Go)
with one addition: a small, compiled-in hook in the emit pipeline that lets
first-party plugins transform the AST during emit, without depending on
`ttsc` or emit patching.

## Status

- Two emit plugins are available, both opt-in via a tsconfig plugins entry
  (see below): **path rewriting** and **bouncer**. With neither configured
  — the default — compiler output is byte-identical to the pinned
  upstream release.
- npm packaging (a `tsc-p` launcher package plus per-platform native
  packages) is under development.

## Path rewriting

The path-rewrite plugin is enabled per project through a ts-patch-style
entry in `compilerOptions.plugins`. It rewrites module specifiers in
emitted JavaScript and declarations so they refer to real emitted files:

- specifiers that resolve through tsconfig `paths` aliases or package.json
  `imports` (`#…`) become relative paths to the resolved file, and
- the emitted extension is derived from the resolved file: `.ts`/`.tsx`/
  `.d.ts` become `.js`, `.mts` becomes `.mjs`, `.cts` becomes `.cjs`, and
  `.json` is kept as-is.

```jsonc
{
    "compilerOptions": {
        "plugins": [
            {
                "name": "@conmoong/path-rewrite",

                // Global default: do rewritten specifiers carry the
                // destination-derived extension? (.json is always kept)
                "extension": true,

                // Also rewrite emitted .d.ts output.
                "declarations": true,

                // true (default) = rewrite everything; false = nothing; or
                // a per-pattern map. Patterns match the written specifier —
                // aliased or relative alike, so "./*" and "../*" address
                // relative imports. paths-style matching: exact match beats
                // star match, longest prefix wins, and unlisted specifiers
                // default to true.
                "alias": {
                    "@app/*": true,
                    "@keep/*": false,
                    "@web/*": { "enabled": true, "extension": false },
                    "./*": true
                }
            }
        ]
    }
}
```

All settings are optional — the entry's presence alone enables the plugin
with the defaults shown. The `plugins` array only selects and configures
compiled-in tsc-p plugins by name; nothing is loaded dynamically, and
entries with other names (editor language-service plugins) are ignored.

Bare package imports that resolve into `node_modules`, unresolved
specifiers, and non-literal specifiers are never touched, and a specifier
that is already correct is left byte-for-byte unchanged. Comments and
source maps are preserved because the rewrite happens in the AST pipeline,
before module lowering (so one rewrite flows into both ESM and CommonJS
output) and after declaration generation.

This includes specifiers the compiler synthesises rather than reads from
source — for example the automatic JSX runtime import generated from a
relative `jsxImportSource` (`"./shim"` → `import { jsx } from
"./shim/jsx-runtime"`). Such a specifier never appears as a written
import/export anywhere in the project, so it doesn't reach the normal
resolution cache; the plugin falls back to a fresh, on-demand resolution
for exactly this case, so it still gets rewritten to a real relative path
with the correct extension (`"./shim/jsx-runtime/index.js"`).

## Bouncer

The bouncer plugin removes declarations or changes how they're exported
based on a `@bouncer` JSDoc tag, for stripping mocks/debug code from
production builds, exposing normally-private helpers to tests, or
reshaping a module's public exports:

```ts
/**
 * @bouncer remove
 */
export function mockNetworkCall() { /* ... */ }

/**
 * @bouncer no-export
 */
export function internalHelper() { /* ... */ }

/**
 * @bouncer export
 */
function testOnlyHelper() { /* ... */ }

/**
 * @bouncer export default
 */
export function primary() { /* ... */ }

/**
 * @bouncer export publicName
 */
function original() { /* ... */ }
```

| Directive | Effect |
|---|---|
| `remove` | Elides the declaration entirely, from every output. |
| `export` | Ensures a plain `export` modifier. |
| `no-export` | Removes the `export` modifier in JavaScript (the implementation stays usable elsewhere in the file); in declaration output, omits the declaration entirely, since it has no public type surface to publish. |
| `export default` | Strips any export from the declaration and appends `export default name;` after it. |
| `export NewName` | Strips any export from the declaration and appends `export { name as NewName };` after it. |
| `release LEVEL` | Marks the declaration's API maturity (`public`, `beta`, `alpha`, or `internal`). Has an effect only when the plugin entry configures a `release` channel — see below. |
| `profile NAME` | Applies whichever of the above the named profile resolves to. |

Enable it the same way:

```jsonc
{
    "compilerOptions": {
        "plugins": [
            {
                "name": "@conmoong/bouncer",

                // This build's release channel: "public", "beta",
                // "alpha", or "internal". Omit to disable release
                // trimming entirely (release tags become a no-op).
                "release": "beta",

                // Named profiles, referenced as "@bouncer profile NAME".
                // A value is "remove", "export", "no-export",
                // "export-default", { "export-as": "NewName" }, or
                // { "release": "LEVEL" }. A profile name with no
                // matching entry here is a safe no-op.
                "profiles": {
                    "prod-hide": "no-export",
                    "test-only": "export",
                    "make-default": "export-default",
                    "rename-thing": { "export-as": "NewName" },
                    "internal-only": { "release": "internal" }
                }
            }
        ]
    }
}
```

### Release channels: `.d.ts` trimming by API maturity

`@bouncer release LEVEL` marks a declaration's maturity — `public` <
`beta` < `alpha` < `internal`, each channel including everything more
stable than it, the same ordering [API Extractor](https://api-extractor.com/)
uses for its `.d.ts` rollups. Building with a configured `release`
channel omits declarations tagged with a *more experimental* level than
the channel from `.d.ts` output only:

```ts
/** Stable, documented entry point. */
export function connect(url: string): Connection { /* ... */ }

/**
 * Under active testing; may still change shape.
 * @bouncer release beta
 */
export function connectWithRetry(url: string, retries: number): Connection { /* ... */ }

/**
 * Not for external use.
 * @bouncer release internal
 */
export function connectRaw(socket: Socket): Connection { /* ... */ }
```

Building with `"release": "beta"` keeps `connect` and `connectWithRetry`
in `.d.ts` and omits `connectRaw`; building with `"release": "public"`
keeps only `connect`. **JavaScript output is never affected** — every
tagged function still runs, regardless of channel; only the published
type surface changes. This makes it a same-source way to ship a stable
public package alongside a richer `@beta`/`@alpha` surface for early
adopters, publishing each from the same tree with only the tsconfig's
`release` value changed between builds. Unlike `remove`/`export`/
`no-export`, `release` has no interaction with the non-exported-
declaration declaration-emit limitation described below — the declaration
transformer's inclusion decision runs first, and `release` trims only
from what it already included.

```ts
/**
 * @bouncer profile prod-hide
 */
export function debugDump(x: unknown) { /* ... */ }
```

Applies to top-level function, class, `const`/`let`/`var`, interface, type
alias, and enum declarations only — nothing nested inside a function,
class, or namespace body. A declaration with no `@bouncer` tag, or one
whose directive text this plugin doesn't recognise (including a profile
name with no configured entry, or `export default`/`export NewName` on a
`const`/`let`/`var` statement with more than one binding, which has no
single unambiguous name), is left completely unchanged. When a
declaration carries more than one `@bouncer` tag, the last one wins.

`export default` and `export NewName` never rename the declaration itself
or touch references to it elsewhere in the file — they only add a
separate export statement after it, referencing its existing name. This
plugin performs no whole-file consistency checking: two declarations both
resolving to `export default` in the same file, or an `export NewName`
colliding with another export, produce invalid JavaScript, and avoiding
that is your responsibility.

`export`/`no-export` toggle the export modifier in both JavaScript and
declaration output — with one inherent limitation, which also applies to
`export default`/`export NewName` on a previously non-exported
declaration: adding an export to a declaration that wasn't already
exported has no effect on `.d.ts` output in any file that has at least
one other import or export (true of essentially every real project
file), because TypeScript's own declaration transformer excludes
non-exported top-level declarations from a module's declaration surface
*before this plugin's declaration hook ever runs* — there is no node
left for the plugin to add an export to. These directives reliably work
in JavaScript output regardless. `remove` and `no-export` are unaffected
by this in the other direction, since they only ever act on declarations
the transformer already decided to include (and `no-export` removes the
declaration outright in declaration output, rather than needing to add
anything to it).

**Mitigation.** The declaration transformer decides what's part of the
public surface by whether a name is referenced from *any* export
statement in the file — not by whether the declaration keyword itself
says `export`. So a manual, otherwise-unused export statement referencing
the name is enough to make the declaration transformer include and
export it directly, independently of this plugin, sidestepping the
limitation entirely:

```ts
/**
 * @bouncer export
 */
class ABC {
    value = 1;
}

export type { ABC };
```

```ts
// emitted .d.ts:
export declare class ABC {
    value: number;
}
export type { ABC };
```

The `type` keyword controls only whether the statement *also* re-exports
the runtime value in JavaScript, not whether the type appears in
`.d.ts` — both `export type { ABC };` and a plain `export { ABC };` cause
inclusion equally. Use `export type { ABC };` when `@bouncer export`
already handles the JavaScript-side export (as above) — a plain
`export { ABC };` there would just be a redundant second value export of
the same name. Omit the `@bouncer export` tag entirely and keep only
`export type { ABC };` if you want the opposite: the type visible to
consumers for annotations, but the implementation truly inaccessible at
runtime.

As with path rewriting, correctness across files is your responsibility:
if another file imports a name this plugin removes, un-exports, or
renames, that import breaks at runtime (module resolution is static in
ESM) — the type checker validates the original, unmodified signatures
before this plugin's AST rewrite runs at emit time, so it cannot catch
this for you.

## Tree-shakability annotations: @conmoong/pure

Bundlers can only drop an unused top-level initialiser if it is marked
side-effect free — the `/*#__PURE__*/` convention that plain `tsc` has
never emitted (microsoft/TypeScript#13721, open since 2017). With a
`{"name": "@conmoong/pure"}` entry in `compilerOptions.plugins`, a `@pure`
JSDoc tag on a top-level variable statement annotates every call and `new`
expression its initialisers evaluate at module load:

```ts
/** @pure */
export const registry = createRegistry(defaults());
```

```js
export const registry = /*#__PURE__*/ createRegistry(/*#__PURE__*/ defaults());
```

Only the module-evaluation spine is annotated — nothing inside function or
class bodies. The tag is the author's explicit assertion, never a
heuristic: untagged statements are untouched, and unconfigured projects
emit byte-identically to upstream. Annotations survive module lowering
(they appear inside CommonJS output too).

## Conditional compilation: @conmoong/paris

"Avec des si on mettrait Paris en bouteille." C++-style conditional
compilation and value substitution, written as ordinary library calls that
remain valid TypeScript everywhere:

```ts
import { ifEq, ifTruthy, value } from "@conmoong/paris";

console.log("version " + value("VERSION"));
ifEq("MODE", "prod", () => {
    ifTruthy("TELEMETRY", () => {
        enableTelemetry();
    });
});
```

Compiled with tsc-p and the plugin configured, conditions are evaluated at
build time: a true condition's body survives as a plain block statement
(same scoping as the callback, so the IDE, an uncompiled run, and the
compiled output all report identical errors), a false condition disappears
along with everything inside it (inner conditions in a pruned branch are
never evaluated, like `#ifdef` guards), `value(name)` becomes a literal,
and the `@conmoong/paris` import is removed. Without the plugin, the
runtime package makes calls **throw** unless `CON_MOONG_PARIS_EN=BOUTEILLE`
(exact case) explicitly opts in to evaluating `process.env` at runtime.

Both ways of reaching the runtime package are recognised identically —
`import { ifEq } from "@conmoong/paris"` and CommonJS
`const { ifEq } = require("@conmoong/paris")` (whole-module
`const paris = require("@conmoong/paris")` too), including a renamed
destructured binding (`const { ifEq: eq } = require(...)`). This is what
lets the plugin apply to plain `allowJs` JavaScript sources as well as
`.ts`, not only TypeScript files that happen to use `require()`.

```jsonc
{
    "compilerOptions": {
        "plugins": [{
            "name": "@conmoong/paris",
            "define": {
                "MODE": "prod",                                     // literal (string/number/bool)
                "LEVEL": { "value": 5 },                            // any JSON via {value}
                "GIT_SHA": { "from": "env", "name": "CI_COMMIT" },  // build-time environment
                "VERSION": { "from": "file", "path": "VERSION.txt", "type": "string" },
                "BLOB": { "from": "file", "path": "logo.bin", "type": "uint8array" }, // or "buffer", "json"
                "FEATURE_X": { "from": "dot_env" },                 // ./.env by default
                "MY_APP_*": { "from": "env" },                      // prefix import
                "*": { "from": "dot_env" }                          // import everything
            }
        }]
    }
}
```

Predicates: `ifDef`/`ifNotDef`; `ifEq`/`ifNotEq` (strict `===` across
types — number `123` never equals string `"123"`; env/file-sourced values
are strings); `ifMatch`/`ifNotMatch` (regex literal, evaluated in RE2);
`ifGt`/`ifGte`/`ifLt`/`ifLte` (numbers or numeric strings);
`ifTrue`/`ifFalse` (strictly boolean); `ifTruthy`/`ifNotTruthy`
(allow-list: non-zero numbers, `"true"`, `"on"`, `"yes"`,
case-insensitive). Nesting is supported. Precedence for colliding define
keys: exact name > prefix wildcard > `"*"`.

The plugin is deliberately explicit-and-loud — these are build failures,
never silent fallbacks: invalid configuration (missing file, bad JSON,
unknown source), non-literal variable names, a condition call used as a
value, non-inline/async/parameterised callbacks, `return` or `var` in a
body (in a block they would change meaning), type-mismatched comparisons,
JS-only regex syntax RE2 cannot evaluate, and any predicate other than
`ifDef`/`ifNotDef` on an undefined variable (guard with nesting).

## Module-graph analysis: @conmoong/graph + @conmoong/graph-validate

"Linters check files; tsc-p checks the graph." `{"name": "@conmoong/graph"}`
turns on import-cycle, boundary/layering, phantom-dependency,
dev-dependency-leak, and unused-file/dependency checks over the whole
compiled module graph — never style or correctness lint, which stays
[tsgolint](https://github.com/typescript-eslint/tsgolint)/oxlint's job.
It is registered last among tsc-p's plugins, after pathrewrite, bouncer,
paris and pure, so checks always see the final, post-transform module
surface.

```jsonc
{
    "compilerOptions": {
        "plugins": [{
            "name": "@conmoong/graph",
            "emit": true,
            "rules": [
                { "module": "some_npm_library", "phantomImport": "error" },
                { "module": "./*", "cycle": "error" },
                { "module": "./src/*", "devLeak": "error" },
                { "module": "./src/features/*", "unused": "error" },
                { "module": "./src/features/*", "importRules": [
                    { "module": "./src/other-features/*", "severity": "error" }
                ] }
            ]
        }]
    }
}
```

The plugin does exactly one of two things, chosen by `"emit"`:

- **`"emit"` set** (`true`, or a custom output path string — default
  `<tsconfig-basename>.graph.json` next to the tsconfig): writes a
  **fact-only** JSON artifact — files, exports, and every resolved edge,
  including the specific named bindings reached (useful as a future
  bundler tree-shaking input, not only for these checks) — and performs
  no rule evaluation itself, even if `"rules"` is also configured
  (printing a one-time note that evaluation is deferred). `"rules"` can
  still be written once, here, for the separate `@conmoong/graph-validate`
  tool to read later.
- **only `"rules"` set** (no `"emit"`): tsc-p evaluates them itself as
  ordinary build diagnostics — including `unused`, once every file in the
  compilation has been processed (a single project's own build always
  sees its whole graph, so no separate tool is needed). If `"emit"` is
  also set, `unused` specifically gets its own note explaining it can
  never be enforced there — unlike the other checks, which just wait for
  a later `@conmoong/graph-validate` run, `unused` requires a workspace
  view broader than any one project's own facts to be meaningful across
  `references`, so it only makes sense once, not deferred-then-repeated.

A `"module"` pattern uses tsconfig `"paths"`'s single-`*` wildcard syntax;
`"./"`/`"../"`-prefixed patterns match project-relative files, anything
else (e.g. `"@babel/*"`) matches a *resolved* package name — so
`"@babel/*"` catches a deep import like `@babel/core/lib/foo` too. The
most-specific pattern wins; entries sharing the exact same pattern merge,
later fields overriding earlier ones. `unused` deliberately does not try
to infer a package's public entry point(s) from `package.json`
`main`/`exports` (those point at compiled output paths, not the source
files the graph tracks, and reverse-mapping that reliably would be
exactly the kind of inferred, hard-to-debug magic this project avoids
elsewhere) — give an entry file its own `"unused": "allow"` rule instead.

For a monorepo with tsconfig `"references"`, `@conmoong/graph-validate`
(a separate, standalone npm package — not part of the tsc-p binary) walks
`references` transitively, combines each sub-project's own rules with the
root's, and stitches cross-package cycles together using each project's
own declared `package.json` name. See that package's own README for its
full CLI usage, the rule-combination/re-scoping rules, and the complete
`.graph.json` fact schema.

## Running on other platforms: the WASI fallback

`tsc-p`'s native binaries cover macOS, Linux and Windows on arm64/x64. For
anything else, `@conmoong/tsc-p-wasi` runs the compiler as a WebAssembly
module (`wasip1`/`wasm`) via Node's built-in `node:wasi` — **no separate
WASI runtime (wasmtime, wasmer, ...) needs to be installed**, only the
same Node you already need to run `npx`.

It is **not** installed automatically (it is not listed in the root
package's `optionalDependencies`), so supported-platform installs never
pay for the extra ~50MB download. Install it explicitly alongside the
root package:

```sh
npm install --save-dev @conmoong/tsc-p @conmoong/tsc-p-wasi
```

With both installed, `tsc-p`'s own CLI automatically falls back to it
whenever the current platform has no native package — no configuration
needed. It also works completely standalone:

```sh
npx tsc-p-wasi --version
npx tsc-p-wasi -p tsconfig.json
```

Same CLI, same `tsconfig.json` handling, same exit codes, and real
filesystem access — WASI's preopens are configured to mirror the host
filesystem directly, the same as a native binary would see. The trade-off
is speed: WebAssembly instantiation and interpretation is measurably
slower to start and run than a native binary, so prefer a native platform
package whenever one exists.

## Pinned upstream release

| | |
|---|---|
| TypeScript version | 7.0.2 |
| Upstream tag | none yet — tracking a commit on `microsoft/TypeScript`'s `main` (see [RELEASING.md](tsc-p/docs/RELEASING.md)'s versioning policy) |
| Upstream commit | `2bd066d87f5bafd315be9f40889d0a60b9e58e0b` |
| Go toolchain | 1.26 (from `tsc/go.mod`) |

## Building from source

Requires Go 1.26 or later and Node 20.19 or later; end users of the npm
packages need neither.

```sh
npm run tscp:build              # native binary for this platform -> built/tscp/bin/
npm run tscp:test               # Go suites + launcher tests
npm run tscp:package            # assemble npm packages and tarballs
npm run tscp:smoke              # clean-install the tarballs and compile a fixture
npm run tscp:release:dry-run   # the full local release rehearsal
```

The binary is self-contained: the TypeScript standard-library declaration
files are embedded via `go:embed`, so it runs from any directory. See
[tsc-p/docs/BUILDING.md](tsc-p/docs/BUILDING.md) for details, including how
the npm launcher and platform packages work.

## Modifications to upstream

The fork is minimal by construction. The only upstream files modified are:

- `tsc/internal/compiler/emitter.go` — two emit-plugin hook points (script
  emit before module lowering; declaration emit before printing)
- `tsc/internal/compiler/emitHost.go` — forwards optional emit plugins from
  the compiler host, applies the tsconfig-configured tsc-p defaults, and
  exposes a resolution-cache lookup for plugins
- `tsc/internal/core/version.go` — pins the reported compiler version to
  the stable release tsc-p tracks
- `package.json` — adds `tscp:*` scripts (build/package/test/publish);
  every existing script is untouched
- `.gitattributes` — merge rules that keep this README and the
  `Herebyfile.mjs` release-profile pin (below) in place across upstream
  merges
- `Herebyfile.mjs` — pins the native-preview release profile to tsc-p's
  tracked stable version instead of upstream's own prerelease/nightly
  profile
- `README.md` — this file replaces the upstream readme (see the upstream
  repository for the original)

Each modified Go file carries a prominent `tsc-p modification` comment at
the change site, as required by the Apache License 2.0. Everything else
tsc-p adds lives under `tsc/internal/tscp/`; see
[tsc/internal/tscp/doc.go](tsc/internal/tscp/doc.go) for the plugin
architecture and the rules for adding new plugins.

## Upstream synchronisation

Maintainers update the fork by merging the next stable upstream release
tag, not by rebasing:

```sh
git config merge.ours.driver true   # one-time per clone
git fetch upstream --tags
git merge v7.x.y   # or a specific commit on upstream/main if 7.x has no tag yet
```

The `.gitattributes` merge rules keep this readme and the release-profile
pin in place automatically during such merges. A nightly canary workflow
additionally merges the patch onto upstream main, tests it, and maintains
the disposable `nightly` branch; a weekly workflow opens an issue when a
new upstream stable release appears. Full procedures:

- [tsc-p/docs/UPSTREAM-SYNC.md](tsc-p/docs/UPSTREAM-SYNC.md) — branch
  model, sync steps, automation
- [tsc-p/docs/RELEASING.md](tsc-p/docs/RELEASING.md) — versioning, the
  release workflow, npm trusted-publishing bootstrap
- [tsc-p/docs/BUILDING.md](tsc-p/docs/BUILDING.md) — build, test, package
  and verification commands

## Licence

TypeScript-Go is licensed under the Apache License 2.0. This fork retains
the upstream [LICENSE](LICENSE) and [NOTICE.txt](NOTICE.txt) unchanged, and
all third-party notices are preserved. tsc-p's modifications are provided
under the same licence. TypeScript and the TypeScript logo are trademarks
of Microsoft; their use here is only to describe compatibility and origin,
and does not imply endorsement.
