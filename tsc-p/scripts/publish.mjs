// Publishes the packed npm packages, safely rerunnable:
//
//   - platform packages are published first, then the WASI package, the launcher last
//   - a package@version that already exists in the registry is verified
//     against the local tarball checksum and then skipped (npm versions are
//     immutable; a mismatch aborts the release)
//   - before the launcher is published, every platform package AND the WASI
//     package must exist in the registry at the release version,
//     and a clean-room install of the root tarball must resolve the
//     just-published current-platform package and compile a fixture
//
// The dist-tag is derived from the manifest version: a version containing
// "-" (a prerelease) publishes under "next", otherwise "latest".
//
//   node tsc-p/scripts/publish.mjs [--dry-run]

import assert from "node:assert";
import crypto from "node:crypto";
import fs from "node:fs";
import os from "node:os";
import pathUtils from "node:path";
import { fail, localBinPath, packagesOutDir, getManifest, runCapture } from "./lib.mjs";

const manifest = getManifest();
const dryRun = process.argv.includes("--dry-run");
const distTag = manifest.version.includes("-") ? "next" : "latest";

const packs = JSON.parse(fs.readFileSync(pathUtils.join(packagesOutDir, "packs.json"), "utf8"));
const packByName = new Map(packs.map(p => [p.name, p]));

function localShasum(filename) {
    const bytes = fs.readFileSync(pathUtils.join(packagesOutDir, filename));
    return crypto.createHash("sha1").update(bytes).digest("hex");
}

function registryShasum(name) {
    try {
        return runCapture("npm", ["view", `${name}@${manifest.version}`, "dist.shasum"], { stdio: ["ignore", "pipe", "ignore"] }).trim() || null;
    }
    catch {
        return null;
    }
}

function publish(pack) {
    const existing = registryShasum(pack.name);
    if (existing !== null) {
        const local = localShasum(pack.filename);
        if (existing !== local) {
            fail(`${pack.name}@${manifest.version} already exists in the registry with a DIFFERENT checksum `
                + `(registry ${existing}, local ${local}). npm versions are immutable; bump the version.`);
        }
        console.log(`skip ${pack.name}@${manifest.version}: already published with matching checksum`);
        return;
    }
    const args = ["publish", pathUtils.join(packagesOutDir, pack.filename), "--access", "public", "--tag", distTag];
    if (dryRun) {
        console.log(`[dry-run] npm ${args.join(" ")}`);
        return;
    }
    console.log(`publishing ${pack.name}@${manifest.version} (dist-tag ${distTag})`);
    runCapture("npm", args, { stdio: "inherit" });
}

// --- 1. platform packages ----------------------------------------------------

const platformPacks = [];
for (const goTarget of manifest.goTargets) {
    const packageName = `@conmoong/${goTarget.basename}`;
    const pack = packByName.get(packageName);
    if (!pack) {
        fail(`platform package ${packageName} is not in packs.json; run package.mjs first`);
    }
    platformPacks.push(pack);
}
for (const pack of platformPacks) {
    publish(pack);
}

// --- 2. verify every platform package is in the registry ----------------------

if (!dryRun) {
    for (const pack of platformPacks) {
        const shasum = registryShasum(pack.name);
        if (shasum === null) {
            fail(`${pack.name}@${manifest.version} is not visible in the registry after publishing`);
        }
    }
    console.log("all platform packages verified in the registry");
}

// --- 3. clean-room install of the root tarball --------------------------------

const rootPack = packByName.get('@conmoong/tsc-p');
if (!rootPack) {
    fail("root package missing from packs.json");
}

if (!dryRun) {
    const dir = fs.mkdtempSync(pathUtils.join(os.tmpdir(), "tscp-publish-check-"));
    try {
        fs.writeFileSync(pathUtils.join(dir, "package.json"), JSON.stringify({ name: "publish-check", private: true }));
        console.log("clean-room install of the root tarball (platform package resolved from the registry)");
        runCapture("npm", ["install", "--no-audit", "--no-fund", pathUtils.join(packagesOutDir, rootPack.filename)], { cwd: dir });
        // Deliberately relative to `cwd: dir` — see localBinPath's doc
        // comment for why an absolute path here would be fragile on Windows.
        const binary = localBinPath("tsc-p");
        const version = runCapture(binary, ["--version"], { cwd: dir }).trim();
        assert.match(version, new RegExp(manifest.typescriptVersion.replace(/\./g, "\\.")), `unexpected --version: ${version}`);
        fs.mkdirSync(pathUtils.join(dir, "src"));
        fs.writeFileSync(pathUtils.join(dir, "src", "main.ts"), "export const answer: number = 42;\n");
        fs.writeFileSync(pathUtils.join(dir, "tsconfig.json"), JSON.stringify({
            compilerOptions: { module: "esnext", target: "es2022", rootDir: "src", outDir: "dist", strict: true },
            include: ["src"],
        }));
        runCapture(binary, ["-p", "."], { cwd: dir });
        assert.ok(fs.existsSync(pathUtils.join(dir, "dist", "main.js")), "clean-room compile produced no output");
        console.log(`clean-room check passed (${version}${manifest.currentPlatform ? `, platform ${manifest.currentPlatform.name}` : ''})`);
    }
    finally {
        fs.rmSync(dir, { recursive: true, force: true });
    }
}

// --- 4. root launcher ----------------------------------------------------------

publish(rootPack);
console.log(
    dryRun
        ? "dry run complete; nothing was published"
        : `published @conmoong/tsc-p@${manifest.version}, ${platformPacks.length} platform packages`,
);
