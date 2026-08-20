# @conmoong/tsc-p-wasi

> **tsc-p is an unofficial downstream fork of
> [microsoft/typescript-go](https://github.com/microsoft/typescript-go)
> (TypeScript 7). It is not affiliated with, maintained by, or endorsed by
> Microsoft.**

The universal fallback for platforms tsc-p's native binaries don't cover
(macOS, Linux and Windows on arm64/x64). This package runs tsc-p as a
WebAssembly module (`wasip1`/`wasm`) via Node's built-in `node:wasi` —
**no separate WASI runtime (wasmtime, wasmer, ...) needs to be
installed**, only the same Node you already need to run `npx`.

## Installation

This package is **not** installed automatically by `@conmoong/tsc-p`
— its `optionalDependencies` only ever resolve one of the native
per-platform packages. Install it explicitly:

```sh
npm install --save-dev @conmoong/tsc-p @conmoong/tsc-p-wasi
```

With both installed, `@conmoong/tsc-p`'s own `tsc-p` command
automatically falls back to this package when your platform doesn't
match any native optionalDependency — no configuration needed.

## Standalone use

```sh
npx tsc-p-wasi --version
npx tsc-p-wasi -p tsconfig.json
```

Behaves exactly like the native binary: same CLI, same `tsconfig.json`
handling, same exit codes, real filesystem access (WASI preopens are
configured to mirror the host's own filesystem).

## Trade-offs versus the native binary

WebAssembly execution is slower to start (Wasm instantiation + Node's own
startup) and the whole runtime — including the TypeScript standard
library — has to be interpreted/JIT-compiled from a single ~50MB
`.wasm` file rather than a native executable, so expect noticeably slower
cold starts on large projects. Prefer a native platform package whenever
one exists for your platform; this package exists specifically for
everything else.

## Licence

Apache-2.0. This package retains the upstream LICENSE and NOTICE.txt.
