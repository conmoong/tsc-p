# Upstream synchronisation

tsc-p tracks stable releases of
[microsoft/TypeScript](https://github.com/microsoft/TypeScript)'s `tsc/`
native compiler. The fork is minimal by construction — the only modified
upstream files are the two emit hook points in `tsc/internal/compiler`,
`tsc/internal/core/version.go`'s version pin, the root `package.json`
scripts, `.gitattributes`, `README.md`, and `Herebyfile.mjs`'s
release-profile pin — so syncs are normally conflict-free.

## Branch model

| Branch | Role |
|---|---|
| `main` | Pristine mirror of upstream main. Never patched; fast-forward only. |
| `dev` | The default branch: upstream stable release + the tsc-p patch. All work happens here. |
| `nightly` | Disposable nightly canary output: latest upstream main + the patch. Force-pushed by automation; never commit to it and never branch from it. |

## Version/tag policy

`tsc-p/manifest.json`'s `upstream.tag` records the stable release tag tsc-p
tracks on `upstream.repository`, or `null` when no such tag exists yet and
tsc-p is instead tracking a specific commit on upstream's `main`. Per the
versioning policy in that file's own `$comment` (enforced by the release
workflow): `.version` may only be a stable `X.Y.Z` when `upstream.tag` is
non-null; whenever it's `null`, `.version` must carry a prerelease suffix
(`X.Y.Z-rc.N`, published under the npm `next` dist-tag, never `latest`).

## Syncing to a new stable release

Triggered by the weekly watch workflow opening an "Upstream TypeScript
X.Y.Z released" issue, or manually:

```sh
git config merge.ours.driver true          # one-time per clone
git remote add upstream https://github.com/microsoft/TypeScript  # one-time
git fetch upstream --tags

git checkout -b sync/ts-X.Y.Z dev
git merge vX.Y.Z
```

If upstream has no stable tag yet for the version you're syncing to,
merge `upstream/main` at a specific commit instead of a tag, and follow
the prerelease branch of the version policy above.

Then:

1. Resolve any conflicts. `README.md` and `Herebyfile.mjs` resolve
   themselves via the merge driver (see `.gitattributes`); expect real
   conflicts only if upstream touched the hook sites in
   `tsc/internal/compiler/emitter.go` or `emitHost.go`.
2. Review upstream release notes, plus packaging deltas:
   `git diff <old-ref>..<new-ref> -- packages/typescript Herebyfile.mjs tsc/go.mod`.
   Mirror anything relevant (new platform targets, Node engine changes) in
   `tsc-p/manifest.json` and the launcher.
3. Run everything: `npm run tscp:release:dry-run`.
4. Re-run the byte-parity check against a pristine binary built from the
   bare upstream ref (the nightly canary does the same automatically
   against upstream main).
5. Update `tsc-p/manifest.json`: `typescriptVersion`, `upstream.tag`,
   `upstream.commit`, `goVersion` if `tsc/go.mod` changed, and `version`
   per the policy above (minor bump for an upstream update, staying
   prerelease if `upstream.tag` is still `null`, dropping the prerelease
   suffix the moment it becomes a real tag).
6. Open a pull request into `dev`; merge when CI is green; release per
   [RELEASING.md](RELEASING.md).

Also sync `main`: `git checkout main && git merge --ff-only upstream/main`.

## Automation

Two scheduled workflows keep human intervention minimal:

- **Nightly canary** (`tsc-p-nightly.yml`, ~00:30 AEST): merges `dev`
  onto the latest upstream main in a throwaway branch, builds, runs all
  tsc-p test suites, byte-compares emit output against a pristine upstream
  build, then force-pushes the result to `nightly` and uploads nightly
  binaries (7-day retention). On failure it opens or updates a single issue
  labelled `nightly-canary`, and closes it again once green. It never
  merges into `dev` and never publishes.
- **Weekly watch** (`tsc-p-upstream-watch.yml`, Monday morning AEST):
  compares the newest `v*` stable tag with the pinned manifest
  version and opens one issue labelled `upstream-release` when a sync is
  due.

### Handling a new nightly canary conflict

The `nightly-canary` issue is read-only signal — it never touches `dev` on
its own. When it reports a new conflict:

1. Reproduce it locally, the same way the workflow does:
   ```sh
   git fetch upstream main
   git checkout -b canary-repro dev
   git config merge.ours.driver true   # so .gitattributes exceptions apply, same as CI
   git merge --no-edit upstream/main
   ```
   Clean up afterwards: `git checkout dev && git branch -D canary-repro`
   (abort first with `git merge --abort` if still mid-merge — if that fails
   on an unrelated submodule path, `git reset --hard dev` on the throwaway
   branch works too).
2. Triage what kind of conflict it is:
   - **Structural, will never resolve itself** (like `Herebyfile.mjs`'s
     release-profile pin — a permanent divergence from whatever upstream
     main is currently prepping, not a real incompatibility): add a
     `merge=ours` entry to `.gitattributes` for that file, with a comment
     explaining why it's permanent. This keeps the canary's signal-to-noise
     high so real conflicts don't get lost in expected ones. Verify with
     the reproduction steps above before committing.
   - **Real conflict in code tsc-p patches** (`tsc/internal/compiler/emitter.go`
     or `emitHost.go`, the two hook sites) — this is the canary doing its
     actual job: upstream changed something the patch touches. Don't paper
     over this with a merge driver.
3. For a real conflict, decide whether to fix `dev` now or defer:
   - Fix now (commit the resolution directly onto `dev`, ahead of any real
     stable-tag sync) when the upstream change is significant enough that
     waiting only lets drift compound — this is the actual value of an
     early-warning canary.
   - Defer, and just track the issue, when it's small and isolated enough
     that re-resolving now risks being wasted work if upstream touches the
     same area again before the next real sync.

Either way, the fix is always a deliberate commit to `dev` that you make
after understanding the conflict — nothing here auto-resolves into a real
branch.

## Upstream workflows on this fork

Forking copies upstream's workflow files; they are handled as follows and
must not be edited or deleted (that would create a permanent conflict
surface):

- `ci.yml` — disabled once via the repository's Actions tab ("Disable
  workflow"); this is stored as a repo setting, not in git.
- `codeql.yml` — self-guarded to `microsoft/TypeScript`; skips on forks.
- `copilot-setup-steps.yml` — `workflow_dispatch` only; never fires.

All tsc-p workflows use distinct `tsc-p-*.yml` file names that upstream
will never collide with.
