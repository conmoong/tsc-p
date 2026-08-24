# Upstream API outlook

Where upstream's programmatic API and extensibility story is heading, and what
it means for tsc-p. This is a snapshot of an actively moving target — it
separates **what is verifiable in the tree today** from **what is planned** from
**what is inference**. Re-check before relying on any of it.

*Last reviewed: 2026-08-25, against `upstream/main` @ `e9e477458d`.*

## Timeline

From upstream's [7.1 iteration plan](https://github.com/microsoft/TypeScript/issues/63703):

| Milestone | Date |
|---|---|
| 7.1 Beta | 2026-09-09 |
| 7.1 RC | 2026-10-20 |
| 7.1 Stable | 2026-11-10 |

"Stabilize API" — Content Mapper, Emit, and Language Service APIs — is a 7.1
work item. Nothing on that plan was marked done at last review.

## What actually exists today (verified)

The API is a **JS client talking to the Go compiler over IPC**, not an
in-process library. `tsc/internal/api/` carries the protocol (msgpack, with a
string table for interning). Reads are lazy: the client holds `NodeHandle`
string references and faults data in on demand, rather than receiving whole
trees. Recent upstream work optimising `RemoteNodeList`/`RemoteNodeArray`
access is consistent with that model.

Emit methods have landed:

```
emit                 emitToString
getJavaScriptEmit    getDeclarationEmit
printNode
transpileModule      transpileDeclaration
```

Two of those matter for extensibility. `getJavaScriptEmit` is the read half of
the custom-transformer plan below, and — worth noting, because it was not
obvious from the roadmap prose — `getDeclarationEmit` means **declaration emit
is exposed too**, not just JS emit.

## What is planned but absent

[Section 3C of the API roadmap](https://github.com/microsoft/TypeScript/issues/63875)
describes custom transformers as:

> fetching the JS emit as a SourceFile (AST), running custom transformations
> client-side, and sending the transformed AST back to the server for final
> emit (basically `printNode`) … may be missing a lot of emit-node metadata
> required to do proper comment preservation and formatting.
>
> *Needed by: Angular, Google, ts-loader — Cost: 2 dev-weeks (less for MVP)*

The building blocks exist; the transformer API itself does not. The ts-loader
maintainer, porting to the 7.1 API, [reports](https://johnnyreilly.com/migrating-ts-loader-to-typescript-7-1-with-ai)
that "custom transformers and project references" are not yet supported and
that `transpileOnly` lacks the APIs it needs — this from one of the three
consumers 3C names.

## Performance: the structural difference

Reads are lazy and cheap. The **write path is not**. `printNode` takes:

```go
type PrintNodeParams struct {
    Data string `json:"data"` // base64-encoded binary AST data
    ...
}
```

A serialized AST blob, base64-encoded — not a node handle. So a transformer
following 3C's design pays, **per file**: encode the AST, base64 it (~33%
inflation), cross the IPC boundary, decode, transform in JS, then encode and
cross back for `printNode`.

There is also no node-factory method in the protocol — no `createNode`,
`updateNode`, or equivalent. The client can *reference* server-side nodes by
handle, but constructing new ones appears to require building the serialized
blob client-side. How ergonomic that is remains to be seen.

**This is where tsc-p differs structurally, not incidentally.** Its plugins run
in-process in Go, inside the real transformer pipeline, on the same AST objects
the compiler already holds. Zero serialization, zero IPC, no encode/decode per
file. That difference does not shrink as upstream optimises the protocol — it
is inherent to in-process versus cross-process.

*Inference, not measurement:* nobody has published benchmarks for the round
trip, and the cost will depend heavily on how much of the tree a transformer
touches. Treat "how much slower" as unknown; only the *direction* is certain.

## What this means for tsc-p

The original framing — "the native compiler has no plugin story" — has a shelf
life. Once 3C ships, JS-authored transformers get an official, supported path,
and existing ts-patch/ttypescript-style transformers get a migration route.

The durable position is narrower but real:

- **In-process, zero-serialization** transforms, for the reason above.
- **Declaration emit.** tsc-p's bouncer (release-channel `.d.ts` trimming,
  export toggling) and pathrewrite operate on declaration output. The API now
  exposes `getDeclarationEmit`, so this is *less* of a differentiator than it
  first appeared — but 3C's round trip is described for JS emit only, and
  whether a transformed declaration AST can be sent back is unestablished.
- **No `runExternalCode`, no child processes, no npm-resolved code executing at
  build time.** Content mappers spawn each mapper package as a child process
  over JSON-RPC, gated behind that flag. tsc-p's plugins are compiled in.

What tsc-p is *not* positioned to be is a general third-party plugin host —
that is precisely what upstream is building, and it will be better supported
there.

## What to watch

| Signal | Why it matters |
|---|---|
| 3C shipping, or slipping past 7.1 | The moment official transformers exist, tsc-p's pitch narrows to performance and declaration emit |
| Whether the round trip covers **declaration** emit | The single change that would most reduce tsc-p's remaining edge |
| Any published benchmark of the transformer round trip | Turns the inference above into a number, in either direction |
| A node-factory API appearing in the protocol | Would signal transformers are being taken seriously as a first-class path |
| `runExternalCode` / content mappers broadening beyond file mapping | Would indicate a general in-compilation plugin host, not just `.vue`-style content |

Related: [#516](https://github.com/microsoft/typescript-go/issues/516) (the
original transformer-plugin request, "Post-7.0"),
[#54276](https://github.com/microsoft/TypeScript/issues/54276) (an earlier
minimal transformer proposal, closed).
