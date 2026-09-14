# Contributing to tsc-p

tsc-p is an unofficial, independent fork of
[microsoft/TypeScript](https://github.com/microsoft/TypeScript)'s `tsc/` native
compiler, adding a compiled-in emit-plugin hook. It is **not** affiliated with
or endorsed by Microsoft. There is no Microsoft CLA here, and Microsoft's
contribution policies do not apply — but see
[Upstream contributions](#upstream-contributions) below, because they do apply
if your change belongs upstream.

## Does your change belong here or upstream?

This matters more than anything else in this document.

**Upstream** — anything reproducible with plain upstream `tsc` and no tsc-p
plugin configured: type checking, emit, module resolution, diagnostics,
performance, watch mode. Report and fix those at
[microsoft/TypeScript](https://github.com/microsoft/TypeScript). Everyone
benefits, and tsc-p inherits the fix automatically at the next upstream sync.

**Here** — the emit-plugin hook, the plugins tsc-p ships
(`@conmoong/path-rewrite`, `@conmoong/bouncer`, `@conmoong/paris`,
`@conmoong/pure`, `@conmoong/graph`), the `@conmoong/*` npm packages, the
launcher, the WASI fallback, and the build/release tooling under `tsc-p/`.

A quick test: run the same project through the matching upstream release. If it
reproduces there, it is upstream's.

## The rule that matters most

**This fork is minimal by construction.** Every upstream file tsc-p modifies is
a permanent merge-conflict surface, so the set is small, enumerated, and
enforced: `tsc-p/scripts/portPatch.mjs` fails if it changes. A change that
requires touching another upstream file is very likely the wrong design. See
[AGENTS.md](AGENTS.md) for the full list and the reasoning.

Adding new *files* under `tsc/internal/tscp/` is fine — new files never
conflict on an upstream merge. Editing existing upstream files is what is
restricted.

## Building and testing

Requires Go 1.26+ and Node 20.19+.

```sh
npm run tscp:build          # cross-compile every target
npm run tscp:test:go        # Go suites (plugins + compiler)
npm run tscp:test:launcher  # launcher tests
npm run tscp:release:dry-run  # the whole pipeline, end to end
```

Full command reference: [tsc-p/docs/BUILDING.md](tsc-p/docs/BUILDING.md).

**Do not declare a change working because a command exited zero.** This project
checks real output: byte-parity of emit against a pristine upstream binary,
clean-room npm installs, executing emitted JavaScript and asserting on stdout,
inspecting actual tarball contents. Match that standard — if you touch the emit
pipeline or packaging, run `npm run tscp:release:dry-run` and read what it
built.

## Pull requests

- Base them on `dev`, the default branch. `release-candidate` is derived and
  force-rebuilt on every port; never target it.
- Use [Conventional Commits](https://www.conventionalcommits.org/)
  (`feat(scope): …`, `fix: …`).
- Keep upstream files untouched unless the change genuinely requires it, and
  say why if it does.
- New plugin behaviour needs a test through the real emit pipeline, not just a
  unit test — see `tsc/internal/tscp/emit_test.go`.

<<<<<<< ours
```bash
npm run -w @typescript/typescript build
npm run -w @typescript/typescript test
npm run -w vscode-typescript build
```
=======
## Use of AI assistance
>>>>>>> theirs

Using AI tools is fine, provided you have read and understood the result and
can discuss and revise it in review like any other contributor. Please say so
in the pull request description.

What is not welcome is bulk, agent-driven output: patches generated across many
issues and forwarded without engagement. That costs more to triage than it
saves.

## Upstream contributions

If your change belongs upstream, follow
[microsoft/TypeScript's own CONTRIBUTING guide](https://github.com/microsoft/TypeScript/blob/main/CONTRIBUTING.md).
Their rules are theirs, not ours, and two are easy to trip over: they require a
signed Microsoft CLA, and they require **explicit disclosure** of AI assistance
— an undisclosed AI-authored PR is closed without review.

<<<<<<< ours
## Before submitting a pull request

Run:

```bash
npx hereby generate
npx hereby build
npx hereby test
npx hereby test:all
npx hereby lint
npx hereby format
npx hereby check:format
npm run -w @typescript/typescript build
npm run -w @typescript/typescript test
npm run -w vscode-typescript build
go -C ./tsc mod tidy -diff
go -C ./tools mod tidy -diff
go work sync
git diff --exit-code
```
=======
## Licence
>>>>>>> theirs

By contributing you agree that your contributions are licensed under
[Apache-2.0](LICENSE.txt), matching the project. `NOTICE.txt` carries upstream's
third-party attributions and is required by Apache-2.0 §4(d) — do not edit or
remove it.
