# Building and testing tsc-p

All commands run from the repository root. Prerequisites for maintainers:
Go 1.26+ (the exact version is pinned by `go.mod`) and Node 20.19+. End
users of the npm packages need neither.

## Everyday commands

| Command | What it does |
|---|---|
| `npm run tscp:build` | Cross-compiles every target in `tsc-p/manifest.json`, including WASI |
| `npm run tscp:test:go` | Runs the Go suites (`tsc/internal/tscp/...`, `tsc/internal/compiler`) |
| `npm run tscp:test:launcher` | Runs the launcher tests (mocha/sinon/chai) |
| `npm run tscp:package` | Assembles npm package directories and tarballs from built binaries into `built/tscp/` |
| `npm run tscp:verify-packages` | Validates package manifests and tarball contents against the manifest |
| `npm run tscp:test:bin` | Compiles and runs a real fixture through each native/WASI binary for the current platform |
| `npm run tscp:test:smoke` | Installs the packed tarballs into a clean temporary project and exercises `tsc-p` end to end, including a path-rewriting project and a bouncer project (both enabled via `compilerOptions.plugins`) whose emitted output is executed, plus a standalone install of the WASI fallback package |
| `npm run tscp:release:dry-run` | All of the above for every target including WASI; the full local release rehearsal |
| `npm run tscp:check:policy` | Checks `manifest.json`'s version against the `upstream.tag` policy (see [RELEASING.md](RELEASING.md)); add `-- --check-npm` to also verify the version is unpublished |
| `npm run tscp:port -- --onto upstream/ts7-release` | Regenerates the `release-candidate` branch: upstream's release lane + the tsc-p patch (see [UPSTREAM-SYNC.md](UPSTREAM-SYNC.md)); `--dry-run` inspects the patch shape without touching branches |

Calling a script directly (e.g. `node tsc-p/scripts/build.mjs`) works
identically to its `npm run` equivalent.

Builds are reproducible: `CGO_ENABLED=0`, `-trimpath`, `-ldflags="-s -w"`,
no embedded timestamps. The binary is self-contained because the TypeScript
standard libraries under `tsc/internal/bundled/libs/` are embedded via
`go:embed`.

## Layout

```
tsc-p/
    manifest.json         single source of truth: version, upstream pin, targets, wasi target, npm names
    packages/tsc-p/        the published tsc-p launcher package (bin + lib)
    packages/tsc-p-wasi/   the published @conmoong/tsc-p-wasi fallback package template (bin + lib)
    scripts/               plain-Node build/package/verify/smoke/publish scripts
    test/                  launcher unit and execution tests (mocha/sinon/chai)
    docs/                  this documentation
tsc/internal/tscp/        all tsc-p compiler-side Go code (hooks + plugins)
built/tscp/                build outputs (never committed)
```

## How the npm distribution works

The root package `tsc-p` contains no binaries. It lists one package per
platform under `optionalDependencies` with **exact** versions; npm installs
only the one whose `platform`/`arch` fields match. The launcher (`bin/tsc-p`)
resolves that package through Node module resolution (including Yarn
Plug'n'Play), finds `tsc-p[.exe]` at that package's own root, and executes
it with all arguments, inheriting stdio and propagating the exit status
(via `process.execve` where available, `execFileSync` otherwise).

Each platform package also declares its own uniquely-named bin entry
(`tsc-p-<platform>-<arch>`, e.g. `tsc-p-darwin-arm64`) pointing at that same root
executable, and carries the full `lib.*.d.ts` standard-library set from
the same source revision, plus LICENSE and NOTICE.txt. This means a user
who already knows their own platform can skip the root package and
resolution entirely: `npm install @conmoong/tsc-p-<platform>-<arch>` then
`npx tsc-p-<platform>-<arch>` execs the native binary directly, no JS involved.

Installing with `--omit=optional` or equivalent is unsupported and produces
a clear error at run time.

### The WASI fallback

A single universal build (`GOOS=wasip1 GOARCH=wasm`, `tsc-p/manifest.json`'s
`wasi` block) is published as `@conmoong/tsc-p-wasi` — **not** listed in the
root's `optionalDependencies`, so it is never installed automatically;
adding it is an explicit, separate step. When the current platform has no
native target, `getExePath.js` throws a distinguishable
`UnsupportedPlatformError`; `main.js` catches specifically that (never any
other `getExePath` failure — a missing platform package on a *supported*
platform is a real installation problem, not something to paper over) and
tries to resolve `@conmoong/tsc-p-wasi` via the same module-resolution
mechanism. If found, it runs the `.wasm` binary in-process via Node's
built-in `node:wasi` (`getWasiFallback.js` + the WASI package's own
`lib/run.mjs`) — no external WASI runtime needed. `preopens: {"/": "/"}`
mirrors the host filesystem, matching a native binary's own access.

One non-obvious fix worth knowing before touching this: WASI preview1 has
no real `getcwd()` syscall, so Go's `wasip1` runtime resolves relative
paths against the `PWD` environment variable it's handed, not any actual
syscall. A parent process that sets a child's working directory via an
API (e.g. Node's `child_process` `cwd` option) changes the real OS-level
cwd but does **not** update an already-inherited `PWD` string — so
`lib/run.mjs` explicitly overrides `PWD: process.cwd()` rather than
passing `process.env` through unchanged. Skipping this silently breaks
every relative path (tsconfig lookups included) for any caller that isn't
a real shell that just `cd`-ed.

## Testing philosophy

- Go integration tests (`tsc/internal/tscp/emit_test.go`) run the real emit
  pipeline with a minimal test plugin and prove hook ordering (script
  before module lowering, declaration after declaration generation),
  source-map/comment preservation, plugin composition, and byte-parity
  with upstream when no plugin is registered.
- Launcher tests cover platform mapping for every supported pair,
  unsupported-platform and missing-package errors, argument fidelity
  (including spaces), exit-code propagation and stdio inheritance.
- `verify-packages` checks versions, `optionalDependencies`, `os`/`cpu`,
  and full tarball contents against an allowlist.
- `smoke` and `bintest` compile and execute real fixtures; nothing is
  declared working on exit code alone.
