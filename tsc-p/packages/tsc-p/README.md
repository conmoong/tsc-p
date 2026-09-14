# tsc-p

> **tsc-p is an unofficial downstream fork of
> [microsoft/typescript-go](https://github.com/microsoft/typescript-go)
> (TypeScript 7). It is not affiliated with, maintained by, or endorsed by
> Microsoft.**

A native TypeScript compiler with a built-in hook for module-specifier
rewriting. The current release performs no rewriting (the hook is an
identity operation) and compiles exactly as the pinned upstream TypeScript
release does.

## Installation

```sh
npm install --save-dev @conmoong/tsc-p
npx tsc-p --version
```

Use `tsc-p` exactly as you would `tsc`; command-line arguments and
`tsconfig.json` behaviour come straight from TypeScript 7.

## How it works

This package contains no binaries itself. It lists one package per
supported platform under `optionalDependencies`, and your package manager
installs only the one matching your operating system and CPU. The `tsc-p`
command then locates that package and runs the native binary inside it.

Because the native binary arrives through optional dependencies,
**installing with `--omit=optional`, `--no-optional`, or an equivalent
package-manager setting is unsupported** and will leave `tsc-p` unable to
run.

## Supported platforms

Native binaries: macOS (arm64, x64), Linux (arm64, x64) and Windows
(arm64, x64). On any other platform, install `@conmoong/tsc-p-wasi`
alongside this package for an automatic WebAssembly/WASI fallback (no
separate WASI runtime needed — just Node's built-in `node:wasi`):

```sh
npm install --save-dev @conmoong/tsc-p @conmoong/tsc-p-wasi
```

Or build from source: https://github.com/conmoong/tsc-p

## Installing a single platform directly

If you already know your exact platform (e.g. a CI job pinned to one
runner), you can skip this package entirely and install the matching
platform package on its own — it declares its own `tsc-p-<os>-<cpu>`
command pointing straight at the native binary, no JS involved:

```sh
npm install --save-dev @conmoong/tsc-p-linux-x64
npx tsc-p-linux-x64 --version
```

Platform package names: `@conmoong/tsc-p-darwin-arm64`,
`@conmoong/tsc-p-darwin-x64`, `@conmoong/tsc-p-linux-arm64`,
`@conmoong/tsc-p-linux-x64`, `@conmoong/tsc-p-win32-arm64`,
`@conmoong/tsc-p-win32-x64`.

## Licence

Apache-2.0. This package retains the upstream LICENSE and NOTICE.txt.
