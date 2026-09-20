# Upstream API outlook

Where upstream's programmatic API and extensibility story is heading, what the
surrounding ecosystem is doing, and what both mean for tsc-p. This is a
snapshot of an actively moving target — it separates **what is verifiable in
the tree today** from **what is planned** from **what is inference**. Re-check
before relying on any of it.

*Last reviewed: 2026-09-20, against `upstream/main` @ `f29aeb9f82`.*

## The thesis

Upstream's product is **type checking**, not emit. Every decision below follows
from that, and it predicts their behaviour better than any reading in which
transformers are merely a backlog item:

- The expensive, universal workload is the language service — every keystroke,
  every editor, every CI run. Emit runs rarely, and increasingly not through
  `tsc` at all.
- Checking is the one capability with no alternative implementation. The emit
  market went to esbuild and swc years ago and was never contested.
- Rewriting in Go with a stdio protocol makes that checking available to any
  editor and any tool in any language — a far larger surface than `tsc`.

The clearest external confirmation is [tsgolint](https://github.com/oxc-project/tsgolint):
oxc, the project most aggressively replacing TypeScript in the emit path, needed
type-aware lint rules, did not write a checker, and instead built a Go binary
linking typescript-go. Emit replaced, checking adopted.

For tsc-p this cuts both ways. Upstream will not ship a competing transformer
story, because it is not their business — so the niche is safe from a rug pull.
Equally, the forces pushing emit away from `tsc` have no counterweight from
Microsoft, so the ground is stable and not growing.

## What exists today (verified)

### A client/server pair, with a synchronous transport

The API is a **JS client talking to the Go compiler over IPC**, not an
in-process library. `tsc/internal/api/` carries the protocol (msgpack, with a
string table for interning); `packages/typescript/src/api/` carries the client.
`tsc --api` runs the stdio server, so **Node is the parent and Go the child**.

Reads are lazy: the client holds `NodeHandle` string references and faults data
in on demand rather than receiving whole trees.

Three additions make the protocol viable for fine-grained consumers:

- **A synchronous client.** `syncChannel.ts` spawns the child and uses
  `fs.readSync`/`fs.writeSync` directly on the pipe file descriptors — real
  synchronous IPC, not worker threads or `Atomics.wait`. This is what lets
  synchronous hosts such as ESLint's parser and rule APIs use the compiler at
  all.
- **Batch requests with pagination.** `BatchRequestsParams` carries
  `Requests`, `ContinuationToken` and `MaxResponseBytesPerPage`, so N calls
  cross the boundary once and a large response pages rather than blows memory.
- **Array overloads on the hot methods.** `getTypeAtLocation` and
  `getSymbolAtLocation` each accept `readonly Node[]` and return an array.
  Bulk querying is in the method shape, not only the transport.

Module resolution is exposed too, via `getResolvedModule` and
`getResolvedModuleFromModuleSpecifier`, returning `resolvedFileName`,
`extension`, `packageId` and `isExternalLibraryImport`.

### Emit is exposed, as text

```
emit                 emitToString
getJavaScriptEmit    getDeclarationEmit
printNode            printFile
transpileModule      transpileDeclaration
```

The distinction that governs everything below: **you can ask the API for emit;
you cannot hook the API's emit.** Output can be requested, and a tree you built
yourself can be printed, but nothing injects a transformer into the compiler's
own emit pipeline.

`getJavaScriptEmit` and `getDeclarationEmit` are **the same handler** with an
`EmitOnly` discriminator, and both return:

```go
type EmitOutputFile struct {
    FileName       string  `json:"fileName"`
    Text           string  `json:"text"`
    SourceFileName *string `json:"sourceFileName,omitempty"`
}
```

Text, not an AST — which is to say, the string you would get by running the
compiler. Everything *around* that read, however, is in place:

- **`printNode` takes `Data string // base64-encoded binary AST data`** with no
  JS/declaration discriminator, and `printFile` sends a whole encoded
  `SourceFile` through it. The decoder reconstructs source-file metadata —
  `IsDeclarationFile`, `languageVariant`, `scriptKind` — so declaration files
  round-trip specifically.
- **A full node factory, client-side.** `packages/typescript/src/ast/` carries a
  6,000-line generated `factory.generated.ts`, a visitor, and `clone`, all
  published from the `typescript` package as `unstable/ast`,
  `unstable/ast/factory`, `unstable/ast/visitor`, `unstable/ast/clone`. The
  factory belongs on the client by design: nodes are built in JS, encoded, and
  shipped, so it was never going to be a protocol method.
- **`createSourceFile` / `createSourceFileFromFile`** parse text into an AST.

The consequence is that a transformer can be assembled **today**, without
upstream adding anything: `getDeclarationEmit` → `createSourceFile` on the
returned text → visit and rebuild with the factory → `printFile`. What is
missing is not the toolkit but the *direct* emit-AST read.

**Measured, not assumed.** Round-tripping a JSDoc-heavy `.d.ts` through
`createSourceFile` → `printFile`:

- **Comments survive re-parse intact** — `@param`, `@returns`, `@throws`,
  `{@link}`, fenced code samples, `@deprecated`, and trailing line comments all
  come back verbatim. Re-parsing does *not* mangle documentation, so a fidelity
  argument resting on JSDoc loss does not hold.
- **Vertical formatting does not.** Every blank line between declarations is
  dropped, and some types are reflowed (a tuple in a type argument explodes
  across lines). For a published `.d.ts` that is a real readability regression,
  though a cosmetic one.
- `preserveSourceNewlines: true` makes it worse rather than better: it does not
  restore the blank lines and it duplicates the leading comment block.
- **A transform currently corrupts the output.** `updateSourceFile` followed by
  `printFile` emits the file's leading comment block a second time, orphaned at
  end of file — with the *unchanged* statement array, so merely reconstructing
  the file triggers it. Minimal repro: `createSourceFile("/** Doc. */\nexport
  declare const a: number;\n")`, `updateSourceFile` with its own statements,
  `printFile` → the doc comment is emitted twice.

That last one is a bug and will be fixed; it is not an architectural limit and
should not be leaned on. The durable difference is narrower: **tsc-p transforms
inside the pipeline before the declaration printer runs**, so it never
reconstructs a tree from text and its output for untouched declarations is
byte-identical to unpatched upstream — which the nightly canary verifies
continuously. Formatting, not documentation, is what the round trip costs.

### Content mappers (shipped)

Foreign file types — `.vue`, `.svelte`, `.astro`, `.mdx` — register a mapper
package in `tsconfig.json`:

```jsonc
{
  "contentMappers": [
    { "package": "vue-ts-mapper", "extensions": [".vue"], "options": {} }
  ]
}
```

Four properties define the design, and each is deliberate:

- **A root-level key**, alongside `references`/`files`/`include` — extensibility
  as project structure, not a compiler knob. It inherits through `extends`.
- **Text in, text out.** `TransformParams{FileName, Content string}` →
  `MappedOutput{Text, Extension, Mappings}`. A preprocessor, not a transformer;
  no AST crosses the wire.
- **Built-in extensions are barred.** Anything in
  `AllSupportedExtensionsWithJson` — `.ts .tsx .js .jsx .mts .cts .mjs .cjs
  .json` and the `.d.*` variants — is rejected with a dedicated diagnostic. A mapper can never
  preprocess ordinary TypeScript.
- **Execution is gated command-line-only.** `runExternalCode` is
  `IsCommandLineOnly: true`; without it, declaring mappers is a hard error. A
  checked-in `tsconfig.json` can never authorise code execution.

Here the compiler is the parent: it spawns each mapper's `exec` (typically
`["node", "./dist/mapper.js"]`) as a long-lived JSON-RPC child, one per mapper
identity, deduplicated by resolved name and version. So **Go parent, Node
children** — the inverse of the API, and the two nest: a Vite build with a Vue
mapper and an API-driven transformer runs Node → Go → Node.

## What is planned but absent

[Section 3C of the API roadmap](https://github.com/microsoft/TypeScript/issues/63875)
describes custom transformers as:

> fetching the JS emit as a SourceFile (AST), running custom transformations
> client-side, and sending the transformed AST back to the server for final
> emit (basically `printNode`) … may be missing a lot of emit-node metadata
> required to do proper comment preservation and formatting.
>
> *Needed by: Angular, Google, ts-loader — Cost: 2 dev-weeks (less for MVP)*

The round trip decomposes into three parts, only one of which is missing:

| Step | Status |
|---|---|
| Read emit as an AST | **Absent** — returns text |
| Transform client-side | Free, it is JS |
| Write back via `printNode` | Present, generic over node kind |

Because the missing piece is kind-agnostic, declarations are not gated behind
JS: if the AST-returning read lands, it very likely lands for both. Two
problems make that harder than the estimate suggests, and neither has a
published design:

- **Emit-node metadata.** For `.js`, losing comment placement is cosmetic. For
  `.d.ts`, JSDoc *is* the product — it is what consumers read on hover.
- **`declarationMap` becomes ill-defined.** Once a client rewrites the
  declaration AST, output positions correspond to nothing the checker saw.
  Composing maps for *content-mapped* inputs — the easy case, with an explicit
  span map to compose — took a full issue and PR ([#63880](https://github.com/microsoft/TypeScript/issues/63880)
  → #63936). Post-hoc AST rewriting has no span map to compose against.

`compilerOptions.plugins` is not a route to any of this. In TS 5 it meant TS
Server **language-service** plugins — editor-only, never affecting `tsc`
output — and its successor is an architecture, not a config key: auxiliary
language servers talking to TypeScript over IPC ([#63800](https://github.com/microsoft/TypeScript/issues/63800)),
now that the compiler serves LSP natively (`tsc --lsp`, `tsc/internal/lsp/`)
rather than through tsserver's bespoke protocol. The field survives only as a
type-check stub. Upstream's position on it is
explicit, in response to [#63975](https://github.com/microsoft/TypeScript/issues/63975):
*"We don't support plugins. We just ignore that entirely, let alone do any
extending."*

## The build shapes

Extension points differ per shape, and `.d.ts` is the axis everything turns on.

| # | Shape | `.js` from | `.d.ts` from | Extension point |
|---|---|---|---|---|
| 1a | `tsc` emits both | tsc | tsc | none |
| 1b | Patched `tsc` (ts-patch) | patched tsc | patched tsc | **dead in TS7** |
| 2 | `tsc --noEmit` + bundler | bundler | separate step | bundler plugin |
| 3 | `tsc --noEmit` + native `.ts` runtime | runtime strips | none | **none** |
| 4 | Foreign sources (`.vue`, `.astro`) | framework tooling | framework tooling | `contentMappers` |
| 5 | Compiler wrapper (ttsc, tsc-p, vue-tsc) | wrapper | wrapper | compiled-in |

Shape 4 used to *require* shape 5 — `vue-tsc`, `svelte-check` and
`astro-check` exist because there was no other way. Content mappers plus
auxiliary language servers are upstream deliberately absorbing that wrapper
ecosystem. They did so for the foreign-file lineage and not for the
transformer lineage, which is a choice rather than a backlog.

Shape 3 is structurally hostile to all transformation — there is no build step
to host one — and it is the fastest-growing shape.

## The surrounding trend

`isolatedModules` → `verbatimModuleSyntax` → `erasableSyntaxOnly` → Node, Bun
and Deno running `.ts` directly all push one direction: **TypeScript as pure
annotation with zero runtime consequence.** The transformer ecosystem is built
on the opposite premise — that types can have runtime effects. The industry is
walking away from that, and TS7 is a symptom rather than a cause.

`isolatedDeclarations` extends the same trend to declaration emit: with
explicit annotations at every export boundary, `.d.ts` can be derived per-file
without inference, which lets oxc generate declarations in Rust with no
TypeScript in the process at all. `rolldown-plugin-dts` lists `typescript` as
an *optional* peer dependency for exactly this reason.

This is the most dangerous pressure on tsc-p, because unlike upstream or a
competing wrapper it does not leave a compiler in the pipeline for tsc-p to
*be*. Its counterweight is the annotation cost, which most codebases have not
paid.

## The competitive set

**[ttsc](https://ttsc.dev)** is the direct peer: a standalone binary built on
typescript-go, plugins compiled into it, shipping `ttsc`, `ttsx`,
`ttscserver` and `@ttsc/unplugin` — one per entry point, because the API only
serves the bundler one. It takes plugin *source*, bundles a Go toolchain, and
compiles plugins into `node_modules/.cache` on first run. That is open-ended
where tsc-p is closed, at the cost of reintroducing build-time execution of
third-party code — the property `runExternalCode` exists to gate.

**`tsc-alias` and api-extractor** are the incumbents that actually matter for
adoption. They post-process declaration *text*: fragile, type-unaware, a
separate step — and good enough for most teams, with years of adoption. Correct
because it is inside the compiler is an argument against tools that already
work.

**Declaration bundling** (rollup-plugin-dts, rolldown-plugin-dts) inlines
internal imports, so alias specifiers disappear rather than needing rewriting.
Path rewriting is therefore a shape-1a concern, not a shape-2 one.

## Two scenarios

**If the AST-returning read lands.** The capability argument goes: JS-authored
declaration transformers become possible, and a wrapper that drives them
follows. What survives is narrower and partly *strengthened*:

- **Trust strengthens.** A JS plugin ecosystem means npm-resolved code
  executing at build time, per project. tsc-p's refusal becomes more
  distinctive, not less.
- **Fidelity is a formatting argument, not a documentation one.** Measured
  above: comments survive a re-parse, blank lines and some type layout do not.
  Worth stating precisely rather than overselling.
- **Delivery is unchanged.** It remains an API; `tsc` gets no hook. Anyone
  wanting "run a compiler, get transformed output" still needs a wrapper.
- **Performance stops being the declaration-side pitch.** Declaration ASTs are
  signatures without bodies — small. The round trip that would be brutal for a
  large implementation file is cheap for its `.d.ts`.

**If it does not land.** The risk is not upstream. It is ttsc adding a
declaration hook, `tsc-alias` and api-extractor remaining good enough, and
`isolatedDeclarations` removing `tsc` from declaration generation entirely.
This scenario's failure mode is a defensible position with no demand.

Both call for the same investment — trust model, declaration fidelity,
single-pass — and for keeping plugin *semantics* separable from the fork.
Semantics port to another host; a fork does not.

## Where tsc-p sits

The durable position is not "we can transform declarations and they cannot".
It is:

> **tsc-p is the only tool where the compiler you invoke is the thing that
> transforms** — config-declared, in-process, `.d.ts`-capable, with no
> `runExternalCode`, no child processes, and no npm-resolved code executing at
> build time.

Concretely that serves **unbundled library authors who need declaration-level
API control and will not run third-party code at build time**. Small, real,
and unserved elsewhere.

It degrades whenever tsc-p is one stage in someone else's pipeline: a
downstream bundler resolves aliases, inlines declarations, and re-emits, so
anything tsc-p *rewrites* gets redone. Only what it *subtracts* survives — a
useful filter for evaluating plugin ideas. For bundler users the coherent
integration is therefore declaration-only: tsc-p replaces the
`tsc --emitDeclarationOnly` step and the bundler keeps JavaScript.

App developers on shapes 2 and 3 get nothing, and that is most bundler users by
headcount.

### tsc-p as an API server: plugins do not fire

The binary accepts `--api` and `--lsp` like any upstream build, so
`new API({ tsserverPath: ".../tsc-p" })` works and `tsc-p --lsp` serves an
editor. Neither runs tsc-p's plugins, and it is worth knowing why before
anyone builds on the idea.

Measured — same binary, same tsconfig, same project:

```
plugins survived into parsed.options: false
CLI  .d.ts contains a @bouncer-removed symbol: no
API  .d.ts contains a @bouncer-removed symbol: YES
```

`createProgram(rootFiles, compilerOptions)` takes a **structured
`CompilerOptions`**, and `plugins` is not one of its fields — it is raw
tsconfig JSON that tsc-p reads separately. `parseJsonConfigFileContent` drops
it in transit, so nothing reaches the activation path. Emit through the API is
otherwise complete and correct; it is simply upstream's emit.

The same holds for the language service, for a simpler reason: hover,
completion and diagnostics never run emit, so emit plugins are invisible to
them by construction. **As an API or LSP server, tsc-p is upstream plus dead
weight.**

Two consequences worth holding:

- The line above is exact rather than rhetorical: the compiler must be
  *invoked*, not *queried*. An API consumer is querying a server.
- `parsed.options` does carry `configFilePath`, so tsc-p *could* re-read the
  raw tsconfig and activate plugins in API mode. That is a decision, not a
  blocker — and a costly one: a tool pointed at tsc-p would silently emit
  differently from the same tool pointed at `tsc`, with nothing in the API
  surface explaining why. If ever done it should be opt-in and loudly
  reported.

### The editor/build divergence, and the gap it exposes

Because plugins are emit-only, the editor shows source truth while the build
ships something else: a `@bouncer remove`d symbol still autocompletes, and a
`paris` branch that will be eliminated still typechecks. For paris that is
correct — you want the disabled branch checked. For bouncer there is a real
hole, already named in its own documentation: removing or un-exporting a name
that another file imports produces a clean editor, a clean typecheck, and a
broken artifact.

The fix belongs in the build, not the editor. `graph` already collects
resolved edges *and the specific named bindings each import reaches for*, and
already reports rule violations as ordinary build diagnostics — so
cross-checking those against what bouncer removed fails the compile before a
broken package can be produced. No editor integration, and no pre-commit hook:
hooks are bypassable, skip CI by default, and would re-parse a program the
compiler already has in memory.

## Portability roadmap

None of the plugins needs the *type checker*. Being inside the compiler buys
module resolution, declaration emit, and a free AST walk. That makes the Go
implementation a delivery choice rather than a technical necessity, and makes
porting the specs feasible.

| Plugin | Needs | Portable off tsc? |
|---|---|---|
| `paris` | Import-binding resolution | **Yes** |
| `pure` | Syntax only | **Yes** |
| `bouncer` | Syntax to decide; declaration emit to apply | JS half only — unsafe, see below |
| `pathrewrite` | Module resolution | Yes, but redundant downstream of a bundler |
| `graph` | Module resolution + an AST walk | Yes — `getResolvedModule` makes a standalone tool plausible |

One JS implementation through `unplugin` reaches Vite, webpack, Rollup,
rolldown, esbuild, Rspack, Farm and Bun; the Rust-core bundlers expose
JS plugin APIs. swc is the exception (Wasm plugins compiled from Rust), and is
not worth a third implementation until someone asks.

Ordered:

1. **Conformance fixtures first.** Tag grammar, edge cases, expected output and
   expected diagnostics, as fixtures any host must pass. Two implementations of
   README prose drift silently; this is what makes it a spec instead of two
   similar tools, and it is cheap now and expensive later.
2. **`@conmoong/paris` as an unplugin.** The only plugin that escapes the
   library-author ceiling — conditional compilation is an app concern, and the
   incumbent (`define`, `DefinePlugin`, `@rollup/plugin-replace`) is untyped
   string substitution with a deserved reputation for replacing text inside
   strings. "Typed, checked, loud `define`" addresses the largest segment in
   the ecosystem.
3. **`@conmoong/pure` as an unplugin** *(optional)*. `/*#__PURE__*/` exists
   solely for bundlers, so that is its natural home, and it is small enough to
   rehearse the spec machinery. Modest market impact; skip it to go straight at
   paris.
4. **`bouncer` × TSDoc interop.** `@bouncer release public|beta|internal`
   already parallels TSDoc's `@public`/`@beta`/`@internal` and api-extractor's
   trimmed rollups. Interoperating widens bouncer *within* the market where it
   is correct.

**Not bouncer as a bundler plugin.** A bundler plugin sees JS only; if it
strips an export while `.d.ts` comes from a separate `tsc` or oxc pass, the two
outputs disagree silently and consumers get a declared export with nothing
behind it at runtime. Bouncer is only correct where one tool owns both outputs.

## What to watch

| Signal | Why it matters |
|---|---|
| `EmitOutputFile` gaining a node handle instead of `Text string` | **The single earliest indicator.** The round trip is blocked on this one change, and it is kind-agnostic — a one-line diff in `tsc/internal/api/proto.go` |
| `unstable/ast` losing its `unstable` prefix | The transformer toolkit becoming a supported surface rather than an experiment |
| `printNode` gaining emit-node metadata fields | Upstream solving the fidelity problem, which is the fallback technical argument |
| Any `declarationMap` story for client-transformed ASTs | The remaining design blocker |
| ttsc shipping a declaration plugin hook | More likely, and sooner, than upstream shipping the round trip |
| `isolatedDeclarations` adoption, and oxc-generated `.d.ts` in defaults | Removes the compiler from the pipeline entirely — the one pressure with no counterweight |
| `runExternalCode` broadening beyond content mappers | A general in-compilation plugin host |

Related: [#63703](https://github.com/microsoft/TypeScript/issues/63703) (7.1
iteration plan — 7.1 Beta 2026-09-09, RC 2026-10-20, Stable 2026-11-10, with
"Stabilize API" a work item),
[#63676](https://github.com/microsoft/TypeScript/issues/63676) (design notes
covering the emit API and content mappers),
[#516](https://github.com/microsoft/typescript-go/issues/516) (the original
transformer-plugin request, "Post-7.0"), and
[#54276](https://github.com/microsoft/TypeScript/issues/54276) (an earlier
minimal transformer proposal, closed).
