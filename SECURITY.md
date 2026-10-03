# Security

tsc-p is an unofficial, independent fork of
[microsoft/TypeScript](https://github.com/microsoft/TypeScript)'s `tsc/` native
compiler. It is **not** affiliated with or endorsed by Microsoft. **Do not
report tsc-p vulnerabilities to Microsoft or to MSRC** — they do not maintain
this software and cannot act on such a report.

## Reporting a vulnerability

**Please do not open a public issue.**

Report privately through
[GitHub Security Advisories](https://github.com/conmoong/tsc-p/security/advisories/new).
That channel is visible only to the maintainers and lets us prepare a fix
before anything is disclosed.

Please include what you can: affected version (`tsc-p --version` and the
`@conmoong/*` package version), platform, a minimal reproduction, and the
impact you believe it has. This is a small project maintained in spare time —
there is no guaranteed response window, though reports are taken seriously.

## Does it belong here or upstream?

tsc-p only adds a compiled-in emit-plugin hook and an npm distribution layer;
everything else is upstream TypeScript.

- **Reproduces with plain upstream `tsc`** (no tsc-p plugin configured) → it is
  an upstream vulnerability. Report it to the
  [Microsoft Security Response Center](https://msrc.microsoft.com/create-report),
  which is the correct channel and is monitored properly. Telling us too is
  welcome so we can pick up the fix, but MSRC should get it first.
- **Requires tsc-p** — one of its plugins, the launcher, the `@conmoong/*`
  packages, or the WASI fallback → report it here.

If you are unsure, report it here and we will help route it.

## Scope

In scope: the tsc-p plugins, the launcher and platform packages, the WASI
fallback, and the release/publish pipeline (for example a supply-chain issue in
how packages are built or published).

Out of scope: vulnerabilities in upstream TypeScript reachable without tsc-p
(report upstream), and issues in the separate `@conmoong/paris`,
`@conmoong/graph-validate` and `@conmoong/teo` repositories (report in those
repositories).
