# Support

tsc-p is an unofficial, independent fork of
[microsoft/TypeScript](https://github.com/microsoft/TypeScript)'s `tsc/` native
compiler. It is **not** affiliated with or endorsed by Microsoft, and Microsoft
does not support it.

## Where to ask

**Is it about the compiler itself?** If the same thing happens with plain
upstream `tsc` — a type error, an emit difference, a crash with no tsc-p plugin
configured — it is an upstream question. Search and report it at
[microsoft/TypeScript/issues](https://github.com/microsoft/TypeScript/issues).
Please don't send upstream compiler questions here; we cannot fix them, and
reporting them upstream helps everyone.

A quick way to tell: run the same project through the matching upstream
release. If it reproduces there, it is upstream's.

**Is it about tsc-p?** Anything involving `compilerOptions.plugins` and the
plugins tsc-p adds (`@conmoong/path-rewrite`, `@conmoong/bouncer`,
`@conmoong/paris`, `@conmoong/pure`, `@conmoong/graph`), the `@conmoong/*` npm
packages, the launcher, or the WASI fallback belongs
[here](https://github.com/conmoong/tsc-p/issues).

**Security vulnerabilities** — see [SECURITY.md](SECURITY.md). Do not open a
public issue.

## Documentation

- [README.md](README.md) — what tsc-p is and how each plugin is configured
- [tsc-p/docs/BUILDING.md](tsc-p/docs/BUILDING.md) — building and testing
- [tsc-p/docs/RELEASING.md](tsc-p/docs/RELEASING.md) — versioning and releases
- [tsc-p/docs/UPSTREAM-SYNC.md](tsc-p/docs/UPSTREAM-SYNC.md) — how the fork tracks upstream

This is a small project maintained in spare time. There is no SLA.
