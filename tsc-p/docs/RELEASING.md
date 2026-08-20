# Releasing tsc-p

## Versioning policy

tsc-p uses its own semantic versioning, independent of TypeScript's. The
upstream TypeScript version is recorded separately in `tsc-p/manifest.json`
and reported by `tsc-p --version` (which prints the upstream TypeScript
version, unchanged). Policy:

- **minor** bump for a new upstream TypeScript release,
- **patch** bump for tsc-p-only fixes,
- **major** bump for breaking changes to tsc-p behaviour or packaging,
- prereleases use `-rc.N` etc. and are published under the `next` dist-tag;
  stable versions use `latest`. Root and platform packages always share the
  same version and dist-tag.

**`.version` may only be a stable `X.Y.Z` when `upstream.tag` in the
manifest is a real, resolvable tag** on `upstream.repository`. Whenever
`upstream.tag` is `null` — tracking a commit on upstream's `main` that has
no stable release tag yet, which is the current state since the upstream
repository consolidation left no `v7.x` tag on `microsoft/TypeScript` —
`.version` must carry a prerelease suffix (e.g. `0.2.0-rc.1`) and publish
under `next`, never `latest`. The release workflow enforces this before
publishing anything.

A release tag `vX.Y.Z` must equal `tsc-p/manifest.json` `.version` exactly;
the release workflow enforces this, along with strict semver validation and
a check that the recorded upstream commit is an ancestor of the release.

## Cutting a release

1. Ensure `dev` is green in CI and `tsc-p/manifest.json` carries the
   intended `version`.
2. Rehearse locally: `npm run tscp:release:dry-run`.
3. Optionally rehearse in CI: run the **tsc-p release** workflow via
   *Run workflow* (workflow_dispatch) — identical pipeline, publishes
   nothing.
4. Tag and push (tags are protected; only maintainers can push them):

   ```sh
   git tag vX.Y.Z
   git push origin vX.Y.Z
   ```

The workflow then, in order: validates the tag; runs all test suites;
cross-compiles every target in the manifest once (binaries are never
rebuilt later); tests every binary on a native GitHub runner (`--version`,
fixture compile, executing the output); assembles and verifies the npm
packages from the downloaded artefacts; publishes the platform packages;
verifies each one is visible in the registry; installs the root tarball in
a clean temporary project so it resolves the just-published platform
package and compiles a fixture; publishes the root package; and finally
creates a GitHub Release with all tarballs and SHA-256 checksums.

### Rerun safety

Releases are idempotent. `publish.mjs` queries the registry before each
publish: an already-published `package@version` whose checksum matches the
local tarball is skipped; a checksum mismatch aborts (npm versions are
immutable — bump the version instead). The root package is never published
until every platform package at the exact version is verified present. The
workflow uses concurrency keyed by tag and never cancels an in-progress
release.

### Native runners

Runner labels per target live in the manifest (`ciRunner`). If GitHub
retires a label, update the manifest. A target with no working native
runner is still cross-compiled and released, but must be called out in the
release notes as not executed in CI.

## npm publishing security

Normal releases use **npm trusted publishing** (GitHub OIDC): no long-lived
`NPM_TOKEN` exists. Requirements already encoded in the workflow: the
`publish` job alone has `id-token: write`, runs in the protected
`npm-publish` environment, and pins the npm CLI (>= 11.5.1).

### One-time bootstrap (before the first release)

Trusted publishing is configured per package, and a package must exist
before a publisher can be configured, so once:

1. Create a short-lived granular npm token (or use an interactive login)
   with publish rights for the `tsc-p` package and the platform scope.
2. Run a local `npm run tscp:release:dry-run`, then publish every package once
   under the non-default `bootstrap` dist-tag:
   `node tsc-p/scripts/publish.mjs` after temporarily setting the dist-tag,
   or `npm publish <tarball> --access public --tag bootstrap` per tarball.
3. On npmjs.com, for **every** package (the root and every native platform
   package): *Settings → Trusted publisher → GitHub Actions*, with the
   repository (`conmoong/tsc-p` — hardcoded in `package.mjs` and the
   package.json templates, see "Renaming the repository" below), workflow
   `tsc-p-release.yml`, environment `npm-publish`.
4. Revoke the bootstrap token.
5. In the GitHub repository settings: create the `npm-publish` environment
   (restrict it to protected tags and required reviewers as desired), and
   protect the `v*` tag pattern.

From then on, releases happen only through protected tags and the
authorised workflow.

## Renaming the repository or moving it to an organisation

Repository identity is deliberately hardcoded rather than manifest-driven
(see AGENTS.md's "tsc-p: npm distribution layer" section) — there is no
single place that centralises it. To rename the repository or transfer it
into a GitHub organisation (with a matching npm organisation/scope):

1. Transfer or rename on GitHub (redirects are kept for remotes and links).
2. Update the hardcoded repository URL in `tsc-p/scripts/package.mjs`
   (the platform packages' `repository.url` literal).
3. Update `homepage`/`bugs`/`repository` in the committed package.json
   templates: `tsc-p/packages/tsc-p/package.json` and
   `tsc-p/packages/tsc-p-wasi/package.json`.
4. If the npm package name or scope changes too: update the
   `@conmoong/` literals in `tsc-p/scripts/*.mjs`
   (`package.mjs`, `verifyPackages.mjs`, `publish.mjs`), the `name` field
   in both package.json templates above, and the static links in
   `README.md` and the two templates' `README.md` files.
5. Update local clones: `git remote set-url origin <new-url>`.
6. If done **before** the first npm publication, nothing else is required —
   run the bootstrap against the new names. If done **after** packages have
   been published, reconfigure the trusted publisher on every package to
   point at the new repository, and treat any package rename as a brand-new
   set of packages (npm names cannot be transferred; publish under the new
   names and deprecate the old ones).

## What a release must report

The release is only done when the workflow run shows: which targets were
built, which were executed on native runners, the registry publications,
the clean-room install check, and the GitHub Release with checksums. Any
step that could not be validated (for example a retired native runner) must
be recorded in the release notes.
