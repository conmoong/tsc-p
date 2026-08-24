# Upstream synchronisation

tsc-p tracks stable releases of
[microsoft/TypeScript](https://github.com/microsoft/TypeScript)'s `tsc/`
native compiler. The fork is minimal by construction — the only modified
upstream files are the two emit hook points in `tsc/internal/compiler`,
`tsc/internal/core/version.go`'s version pin, the root `package.json`
scripts, `.gitattributes`, `README.md`, and `Herebyfile.mjs`'s
release-profile pin — so syncs are normally conflict-free.

## Branch model

tsc-p runs **two lanes**, because upstream does. Upstream's `main` is the next
minor's development line, and its stable tags live only on a release branch
(`ts7-release`) that periodically merges `main` and is then tagged. Those tags
are therefore *not* ancestors of `main`: no amount of merging `main` will ever
reach one. The two lanes follow directly from that topology.

| Branch | Role |
|---|---|
| `dev` | **The default branch.** Upstream `main` + the tsc-p patch. All work happens here. Ships `-edge` prereleases under the npm `next` dist-tag. |
| `sync/upstream-main` | Disposable. CI-merged upstream `main`, the head of the nightly sync PR. Force-pushed nightly. |
| `release-candidate` | Disposable. Upstream's release lane + the tsc-p patch, **regenerated** by `portPatch.mjs`. Stable releases are cut from here. Never commit to it — it is force-rebuilt on every port. |

There is deliberately no pristine `main` mirror: nothing in the tooling needs
one (everything reads `upstream/*` remote-tracking refs directly), and a stale
mirror is worse than none.

Why continuous tracking of `main` rather than following the release branch:
conflicts are cheap taken in small daily deltas and expensive taken in one big
batch. Upstream's release branch sat still for six weeks and then absorbed
`main` in one jump; tracking `main` means every conflict is small and fresh.

## Version/tag policy

`tsc-p/manifest.json`'s `upstream.tag` records the stable release tag tsc-p
tracks on `upstream.repository`, or `null` when no such tag exists yet and
tsc-p is instead tracking a specific commit on upstream's `main`. The rule,
enforced in both directions by `tsc-p/scripts/checkReleasePolicy.mjs`:

| `upstream.tag` | `.version` | npm dist-tag |
|---|---|---|
| `null` | **must** carry a prerelease suffix | `next` |
| a real tag | **must not** carry one | `latest` |

Prerelease suffixes are date-stamped by convention — `0.2.0-edge.20260824` —
so each edge build gets a unique version (npm never allows republishing one)
and they sort correctly ahead of the eventual `0.2.0`. This is a convention,
not something the tooling generates: set `.version` by hand.

Run it any time, not just at release:

```sh
npm run tscp:check:policy
```

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

## Automation

Three scheduled workflows keep human intervention minimal. None of them
publishes anything, and none writes to `dev`: every change reaches `dev`
through a pull request you merge.

- **Nightly canary** (`tsc-p-nightly.yml`, ~00:30 AEST): merges `dev` onto the
  latest upstream `main` in a throwaway branch, builds, runs all tsc-p test
  suites, byte-compares emit output against a pristine upstream build, and
  uploads nightly binaries (7-day retention). When the merge is clean it also
  pushes `sync/upstream-main` and opens (or updates) a PR into `dev` — the
  merge is done **in CI**, because `.gitattributes`'s `merge=ours` rules for
  `README.md` and `Herebyfile.mjs` need `git config merge.ours.driver true`,
  which GitHub's merge button does not have. On failure it opens or updates a
  single issue labelled `nightly-canary`, closing it again once green.
- **Release candidate** (`tsc-p-release-candidate.yml`, daily ~02:00 AEST):
  regenerates `release-candidate` as upstream's release lane + the tsc-p
  patch, then builds and tests it. Skips silently when neither input moved —
  it compares the current `upstream/ts7-release` and `dev` SHAs against the
  `Upstream-Sha:`/`Dev-Sha:` trailers recorded in the previous candidate's own
  commit message, so there is no external state to keep. Reports via an issue
  labelled `release-candidate` when the port stops applying.
- **Upstream release watch** (`tsc-p-upstream-release-watch.yml`, Monday
  morning AEST): compares the newest upstream `v*` stable tag with the pinned
  manifest version and opens one issue labelled `upstream-release` when a sync
  is due. A new tag is what makes a *stable* tsc-p release possible at all.

## The release lane

`release-candidate` is a **derived artifact**, not a maintained branch. Each
port rebuilds it from scratch:

```sh
npm run tscp:port -- --onto upstream/ts7-release --from dev
```

This means nothing diverges and conflicts never accumulate: the only question
ever asked is "does today's patch apply to today's upstream release line?".
It also means **anything you commit to that branch is destroyed on the next
run** — fixes must land in `dev`.

`portPatch.mjs` splits the patch in two. Added paths (`tsc/internal/tscp/**`,
`tsc-p/**`, …) are copied verbatim and cannot conflict textually — though they
can still fail to *compile* if they call an upstream API that differs between
lanes. The modified upstream files are asserted against an explicit allowlist
and applied with a 3-way merge; if that list ever changes, the port fails
loudly rather than silently growing the fork.

Two mechanisms handle genuine divergence between lanes:

- **Exclusions** — for patches upstream owns on that lane. The `version.go`
  and `Herebyfile.mjs` pins exist only because `dev` tracks a development
  line; a release lane already reports a stable version, so porting them
  would fight upstream. Excluded automatically for non-`main` targets.
- **Overlays** — `tsc-p/patches/<lane>/*.patch`, applied after the main patch,
  for a change whose `main`-lane diff cannot apply because the surrounding
  upstream code differs. The file is excluded from the main patch for that
  lane (`LANE_EXCLUDES` in `portPatch.mjs`) and supplied here instead. Each
  overlay records why it exists and when it can be deleted; keep the
  directory as empty as possible, since every entry means the same change is
  expressed twice.

The ported manifest gets `upstream.tag` and `upstream.commit` stamped
automatically — they describe the ref being ported onto, and left alone would
put the wrong provenance in the release notes. `version` is deliberately not
touched: that is your call, and the policy check will reject a real tag paired
with a prerelease version, so a forgotten bump fails loudly.

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
