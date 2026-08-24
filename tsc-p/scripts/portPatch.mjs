// Regenerates the tsc-p patch on top of a different upstream ref, producing a
// release candidate:
//
//   node tsc-p/scripts/portPatch.mjs --onto upstream/ts7-release [--from dev] [--branch release-candidate]
//
// The release lane is a DERIVED artifact, never a hand-maintained branch. Each
// run rebuilds it from scratch, so nothing diverges and conflicts can never
// accumulate: the only question ever asked is "does today's patch apply to
// today's upstream release line?".
//
// The patch has two halves, and only one of them can conflict:
//
//   added files     entirely new paths (tsc/internal/tscp/**, tsc-p/**, ...).
//                   Copied verbatim. Cannot conflict, by construction.
//   modified files  the handful of upstream files tsc-p edits. This is the
//                   whole conflict surface, and it is enumerated below.
//
// MODIFIED_UPSTREAM_FILES is asserted against reality on every run. If the
// fork ever grows an extra modified upstream file, this script fails loudly
// rather than silently porting it -- turning AGENTS.md's "minimal by
// construction" rule into something CI enforces instead of something a
// document merely asks for.

import fs from "node:fs";
import pathUtils from "node:path";
import { fail, repoRoot, run, runCapture, tscpDir } from "./lib.mjs";

// Patches that exist ONLY because the dev lane tracks upstream's development
// line, where the reported version is a moving "X.Y.Z-dev" and the release
// profile is set up for nightlies. tsc-p pins both so its own builds report a
// stable version. An upstream *release* lane already supplies exactly that, so
// porting these would fight upstream over a value it is authoritative for --
// they are excluded by default when the target is not upstream's main.
const DEV_LANE_ONLY_FILES = [
    "Herebyfile.mjs",
    "tsc/internal/core/version.go",
];

// Files whose main-lane diff cannot apply to a given upstream lane because the
// surrounding upstream code differs there. Each is excluded from the main patch
// and supplied instead by a lane override under tsc-p/patches/<lane>/, written
// against that lane's own context. See that directory's README for the why of
// each entry. Keep this empty whenever possible: an entry here means the same
// change is expressed twice and both copies have to be kept honest.
const LANE_EXCLUDES = {
    "ts7-release": [
        // The compilerOptions.plugins/extends fix. On main it neighbours the
        // contentMappers handling, which ts7-release does not have.
        "tsc/internal/tsoptions/tsconfigparsing.go",
        "tsc/internal/tsoptions/tsconfigparsing_test.go",
    ],
};

// Every upstream file tsc-p modifies. Keep in sync with AGENTS.md's table.
const MODIFIED_UPSTREAM_FILES = [
    ".gitattributes",
    ".gitignore",
    "Herebyfile.mjs",
    "README.md",
    "package.json",
    "tsc/internal/compiler/emitHost.go",
    "tsc/internal/compiler/emitter.go",
    "tsc/internal/core/version.go",
    "tsc/internal/tsoptions/tsconfigparsing.go",
    "tsc/internal/tsoptions/tsconfigparsing_test.go",
];

const args = process.argv.slice(2);
function arg(name, fallback) {
    const i = args.indexOf(name);
    return i >= 0 && args[i + 1] ? args[i + 1] : fallback;
}

const onto = arg("--onto");
const from = arg("--from", "dev");
// The upstream ref `from` is built on. The patch is defined as everything
// `from` adds on top of this -- deliberately NOT merge-base(onto, from):
// the two lanes diverged long ago, and in a shallow clone their common
// ancestor is usually unreachable anyway.
const baseRef = arg("--base", "upstream/main");
const branch = arg("--branch", "release-candidate");
const dryRun = args.includes("--dry-run");

if (!onto) {
    fail("usage: node tsc-p/scripts/portPatch.mjs --onto <upstream-ref> [--from <ref>] [--base <ref>] [--branch <name>] [--dry-run]");
}

const git = (...a) => runCapture("git", a, { cwd: repoRoot }).trim();

const ontoSha = git("rev-parse", onto);
const fromSha = git("rev-parse", from);
let base;
try {
    base = git("merge-base", baseRef, from);
}
catch {
    fail(
        `cannot find the fork point of ${from} from ${baseRef}. Fetch enough history for both `
        + `(a shallow clone may need --deepen), or pass an explicit --base.`,
    );
}

console.log(`porting ${from} (${fromSha.slice(0, 10)}) onto ${onto} (${ontoSha.slice(0, 10)})`);
console.log(`patch base: ${baseRef} (${base.slice(0, 10)})`);

// --- 1. work out what the patch actually consists of -------------------------

const changes = git("diff", "--name-status", `${base}`, `${from}`)
    .split("\n").filter(Boolean)
    .map(line => {
        const [status, ...rest] = line.split("\t");
        return { status: status[0], path: rest[rest.length - 1] };
    });

const added = changes.filter(c => c.status === "A").map(c => c.path);
const modified = changes.filter(c => c.status === "M").map(c => c.path).sort();
const deleted = changes.filter(c => c.status === "D").map(c => c.path);

if (deleted.length > 0) {
    fail(
        `the patch deletes upstream files, which tsc-p never does:\n  ${deleted.join("\n  ")}\n`
        + `Deleting upstream files guarantees future merge conflicts; see AGENTS.md.`,
    );
}

const expected = [...MODIFIED_UPSTREAM_FILES].sort();
if (JSON.stringify(modified) !== JSON.stringify(expected)) {
    const extra = modified.filter(f => !expected.includes(f));
    const gone = expected.filter(f => !modified.includes(f));
    fail(
        `the set of modified upstream files does not match MODIFIED_UPSTREAM_FILES.\n`
        + (extra.length ? `  newly modified (fork is growing): ${extra.join(", ")}\n` : "")
        + (gone.length ? `  no longer modified (update the list): ${gone.join(", ")}\n` : "")
        + `tsc-p is minimal by construction: every modified upstream file is a permanent merge-conflict\n`
        + `surface. If a new one is genuinely necessary, add it to MODIFIED_UPSTREAM_FILES here and to\n`
        + `AGENTS.md's table, deliberately.`,
    );
}

// Porting onto upstream's own release line: leave the version/profile pins to
// upstream. --include-dev-pins overrides, --exclude adds more.
const isReleaseLane = !/(^|\/)main$/.test(onto);
const laneName = pathUtils.basename(onto);
const excluded = new Set([
    ...(isReleaseLane && !args.includes("--include-dev-pins") ? DEV_LANE_ONLY_FILES : []),
    ...(LANE_EXCLUDES[laneName] ?? []),
    ...(arg("--exclude", "").split(",").filter(Boolean)),
]);
const toApply = MODIFIED_UPSTREAM_FILES.filter(f => !excluded.has(f));

console.log(`patch: ${added.length} added file(s), ${modified.length} modified upstream file(s)`);
if (excluded.size > 0) {
    console.log(`excluded from this lane (upstream owns these here): ${[...excluded].join(", ")}`);
}

if (dryRun) {
    console.log("dry run: patch shape verified, nothing written");
    process.exit(0);
}

// --- 2. rebuild the candidate from scratch -----------------------------------

// This rewrites the working tree onto a different upstream ref, so anything
// uncommitted would be silently clobbered or block the switch.
if (git("status", "--porcelain")) {
    fail(
        "the working tree has uncommitted changes. Porting rewrites the tree onto a different\n"
        + "upstream ref; commit or stash first (use --dry-run to inspect the patch shape safely).",
    );
}
const startingRef = (() => {
    try {
        return git("symbolic-ref", "--quiet", "--short", "HEAD");
    }
    catch {
        return git("rev-parse", "HEAD");
    }
})();
console.log(`(will leave you on ${branch}; you started on ${startingRef})`);

run("git", ["switch", "--force-create", branch, ontoSha], { cwd: repoRoot });

// Added paths are new by definition, so a verbatim checkout is both correct
// and conflict-free.
if (added.length > 0) {
    for (let i = 0; i < added.length; i += 200) {
        run("git", ["checkout", from, "--", ...added.slice(i, i + 200)], { cwd: repoRoot });
    }
}

// The modified files are the real work. --3way lets git fall back to a proper
// content merge (and leave conflict markers) instead of refusing outright.
const patchFile = pathUtils.join(repoRoot, ".tscp-port.patch");
fs.writeFileSync(patchFile, runCapture("git", ["diff", base, from, "--", ...toApply], { cwd: repoRoot }));

let conflicted = false;
try {
    run("git", ["apply", "--3way", "--whitespace=nowarn", patchFile], { cwd: repoRoot });
}
catch {
    conflicted = true;
}
finally {
    fs.rmSync(patchFile, { force: true });
}

// --- 3. lane-specific overrides ---------------------------------------------
// Escape hatch for a conflict that exists only on this lane (upstream's release
// branch differing from main in a way the patch cannot straddle). Fixes live in
// `from`, so they survive regeneration -- never hand-edit the candidate branch,
// it is force-rebuilt on the next run. Normally this directory does not exist.
const overlayDir = pathUtils.join(tscpDir, "patches", pathUtils.basename(onto));
if (fs.existsSync(overlayDir)) {
    const patches = fs.readdirSync(overlayDir).filter(f => f.endsWith(".patch")).sort();
    for (const p of patches) {
        console.log(`applying lane override ${p}`);
        run("git", ["apply", "--3way", "--whitespace=nowarn", pathUtils.join(overlayDir, p)], { cwd: repoRoot });
    }
}

// --- 4. stamp provenance into the manifest -----------------------------------
// The manifest is an "added" path, so it arrives carrying the source lane's
// values -- which describe a different upstream commit entirely. Left alone,
// the release notes would cite the wrong provenance. tag/commit are mechanical
// so they are stamped here; `version` is a deliberate human decision and is
// left for the release author. checkReleasePolicy.mjs then rejects the
// combination of a real tag with a prerelease version, so a forgotten bump
// fails loudly instead of shipping.
const manifestPath = pathUtils.join(tscpDir, "manifest.json");
const manifest = JSON.parse(fs.readFileSync(manifestPath, "utf8"));
const exactTag = (() => {
    try {
        return runCapture("git", ["describe", "--tags", "--exact-match", ontoSha], { cwd: repoRoot, stdio: ["ignore", "pipe", "ignore"] }).trim();
    }
    catch {
        return null; // the release branch tip is usually untagged between releases
    }
})();
manifest.upstream.commit = ontoSha;
manifest.upstream.tag = exactTag;
fs.writeFileSync(manifestPath, JSON.stringify(manifest, undefined, 4) + "\n");
console.log(`stamped manifest: upstream.commit=${ontoSha.slice(0, 10)}, upstream.tag=${exactTag ?? "null"}`);

// --- 5. commit, recording the exact inputs -----------------------------------
// The two input SHAs live in the commit message as trailers. That is both the
// change-detection mechanism for the daily job (compare against the current
// refs; skip when identical) and free provenance: `git log -1 <branch>` says
// exactly what any candidate was built from, with no external state to lose.
run("git", ["add", "-A"], { cwd: repoRoot });
const message = [
    `port: ${pathUtils.basename(onto)} + tsc-p patch`,
    ``,
    `Regenerated by tsc-p/scripts/portPatch.mjs. This branch is derived and is`,
    `force-rebuilt on every run -- never commit to it directly; fixes belong in`,
    `${from}.`,
    ``,
    `Upstream-Ref: ${onto}`,
    `Upstream-Sha: ${ontoSha}`,
    `Dev-Ref: ${from}`,
    `Dev-Sha: ${fromSha}`,
].join("\n");
run("git", ["commit", "--quiet", "--message", message], { cwd: repoRoot });

if (conflicted) {
    const markers = runCapture("git", ["grep", "-l", "-E", "^<{7} ", "--", ...toApply], { cwd: repoRoot, stdio: ["ignore", "pipe", "ignore"] }).trim();
    console.error("");
    console.error("CONFLICTS: the patch did not apply cleanly to this upstream ref.");
    if (markers) console.error(`files with conflict markers:\n  ${markers.split("\n").join("\n  ")}`);
    console.error(`The candidate was committed anyway so it can be inspected on branch ${branch}.`);
    console.error(`Resolve in ${from} (or add a lane override under tsc-p/patches/), then re-run.`);
    process.exit(2);
}

console.log(`\nport complete on branch ${branch}`);
