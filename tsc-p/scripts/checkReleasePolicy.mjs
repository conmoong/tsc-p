// Validates tsc-p/manifest.json against the release policy before anything
// expensive happens. Safe to run locally at any time:
//
//   node tsc-p/scripts/checkReleasePolicy.mjs [--tag vX.Y.Z] [--check-npm]
//
// --tag       also assert the given git tag equals "v" + manifest .version
// --check-npm also assert the version is not already on the registry
//
// The policy (see tsc-p/docs/RELEASING.md):
//
//   upstream.tag === null   ->  .version MUST carry a prerelease suffix, and
//                               publishes under the npm "next" dist-tag. tsc-p
//                               is tracking an untagged upstream commit, so it
//                               cannot claim to be a stable build.
//   upstream.tag !== null   ->  .version MUST NOT carry a prerelease suffix.
//                               An upstream stable tag is what earns a stable
//                               tsc-p version.
//
// Both directions matter. Without the second, a release-lane build could ship
// an "-edge" version as though it were the tagged release; without the first,
// a dev-lane build could claim stability it does not have.

import { fail, getManifest, runCapture } from "./lib.mjs";

const args = process.argv.slice(2);
const tagIndex = args.indexOf("--tag");
const expectedTag = tagIndex >= 0 ? args[tagIndex + 1] : undefined;
const checkNpm = args.includes("--check-npm");

const manifest = getManifest();
const version = manifest.version;
const upstreamTag = manifest.upstream?.tag ?? null;
const upstreamCommit = manifest.upstream?.commit ?? null;

const problems = [];

// A strict semver, optionally with a prerelease and/or build suffix.
const semver = /^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-([0-9A-Za-z.-]+))?(?:\+([0-9A-Za-z.-]+))?$/;
const parsed = semver.exec(version ?? "");
if (!parsed) {
    problems.push(`version ${JSON.stringify(version)} is not a strict semantic version`);
}
const prerelease = parsed?.[4];

if (parsed) {
    if (upstreamTag === null && !prerelease) {
        problems.push(
            `upstream.tag is null (tracking an untagged upstream commit), so version must carry a `
            + `prerelease suffix (e.g. ${parsed[1]}.${parsed[2]}.${parsed[3]}-edge.YYYYMMDD) and publish `
            + `under the npm "next" dist-tag -- got ${version}`,
        );
    }
    if (upstreamTag !== null && prerelease) {
        problems.push(
            `upstream.tag is ${JSON.stringify(upstreamTag)} (a real upstream release), so version must be `
            + `a stable X.Y.Z with no prerelease suffix -- got ${version}`,
        );
    }
}

if (!upstreamCommit) {
    problems.push("upstream.commit is missing; every build must record the upstream commit it was made from");
}

if (expectedTag !== undefined && `v${version}` !== expectedTag) {
    problems.push(`git tag ${expectedTag} does not match manifest version ${version} (expected v${version})`);
}

// Cheap pre-flight: a version already on the registry is almost always a
// forgotten version bump. Doing this before the build matrix turns a
// ~20-minute failure into a ~5-second one. publish.mjs still does the
// authoritative checksum comparison later, which is what makes a genuine
// re-run of the same release safe.
if (checkNpm && parsed) {
    const packageName = "@conmoong/tsc-p";
    let published;
    try {
        published = runCapture("npm", ["view", `${packageName}@${version}`, "version"], {
            stdio: ["ignore", "pipe", "ignore"],
        }).trim();
    }
    catch {
        published = ""; // not published: the expected, healthy case
    }
    if (published) {
        problems.push(
            `${packageName}@${version} is already on the registry. npm versions are immutable -- bump `
            + `.version in tsc-p/manifest.json (a prerelease needs a fresh suffix, e.g. -edge.YYYYMMDD).`,
        );
    }
}

if (problems.length > 0) {
    for (const problem of problems) {
        console.error(`FAIL ${problem}`);
    }
    fail(`${problems.length} release-policy problem(s) found; see tsc-p/docs/RELEASING.md`);
}

console.log(
    `release policy ok: version ${version}, upstream.tag ${upstreamTag === null ? "null (prerelease lane)" : upstreamTag}, `
    + `dist-tag ${prerelease ? "next" : "latest"}`,
);
