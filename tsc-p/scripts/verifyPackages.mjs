// Validates the assembled npm packages against the manifest: exact versions, complete optional-dependency map, 
// os/cpu fields, and tarball contents (executable, standard libraries, LICENSE, NOTICE.txt, and no unexpected files). 

import fs from "node:fs";
import pathUtils from "node:path";
import {
    libsSourceDir,
    npmOutDir,
    packagesOutDir,
    parseTarListing,
    getManifest,
    runCapture,
} from "./lib.mjs";

const manifest = getManifest();

const problems = [];
function problem(message) {
    problems.push(message);
    console.error(`FAIL ${message}`);
}
function ok(message) {
    console.log(`ok   ${message}`);
}

const packsPath = pathUtils.join(packagesOutDir, "packs.json");
if (!fs.existsSync(packsPath)) {
    console.error("error: no packs.json found; run package.mjs first");
    process.exit(1);
}
const packs = JSON.parse(fs.readFileSync(packsPath, "utf8"));
const packByName = new Map(packs.map(p => [p.name, p]));

const expectedLibCount = fs.readdirSync(libsSourceDir).filter(name => name.endsWith(".d.ts")).length;

function tarballEntries(filename) {
    return parseTarListing(runCapture("tar", ["-tzf", pathUtils.join(packagesOutDir, filename)]));
}

function readPackageJson(pack) {
    return JSON.parse(fs.readFileSync(pathUtils.join(npmOutDir, pathUtils.basename(pack.dir), "package.json"), "utf8"));
}

// --- root launcher -----------------------------------------------------------

const rootPack = packByName.get('@conmoong/tsc-p');
if (!rootPack) {
    problem(`root package @conmoong/tsc-p was not packed`);
}
else {
    const pkg = readPackageJson(rootPack);
    if (pkg.version !== manifest.version) {
        problem(`root version ${pkg.version} != manifest ${manifest.version}`);
    }
    if (pkg.private) {
        problem("root package is still marked private");
    }
    if (!pkg.bin || pkg.bin["tsc-p"] !== "./bin/tsc-p") {
        problem("root package bin entry is wrong");
    }
    if (pkg.preferUnplugged !== true) {
        problem("root package must set preferUnplugged");
    }

    const expectedOptional = {};
    for (const goTarget of manifest.goTargets) {
        // Note: the root launcher only depends on native per-platform
        // packages; wasm targets (the WASI fallback, and any future browser
        // target) are never platform-specific binaries npm can auto-select,
        // so they are deliberately not listed in optionalDependencies.
        // Users who want one must install it themselves.
        if (goTarget.type === "native") {
            expectedOptional[`@conmoong/${goTarget.basename}`] = manifest.version;
        }
    }
    if (JSON.stringify(pkg.optionalDependencies) !== JSON.stringify(expectedOptional)) {
        problem(`root optionalDependencies do not exactly match the manifest target matrix`);
    }    

    const entries = tarballEntries(rootPack.filename);
    for (const required of ["package.json", "README.md", "LICENSE", "NOTICE.txt", "bin/tsc-p", "lib/main.js", "lib/getExePath.js", "lib/getWasiFallback.js"]) {
        if (!entries.includes(required)) {
            problem(`root tarball missing ${required}`);
        }
    }
    const allowed = /^(package\.json|README\.md|LICENSE|NOTICE\.txt|bin\/tsc-p|lib\/[A-Za-z]+\.js)$/;
    for (const entry of entries) {
        if (!allowed.test(entry)) {
            problem(`root tarball contains unexpected file: ${entry}`);
        }
    }
    if (problems.length === 0) {
        ok(`root ${rootPack.filename}: version, bin, optionalDependencies and contents verified`);
    }
}

// --- platform packages -------------------------------------------------------

for (const goTarget of manifest.goTargets) {
    const packageName = `@conmoong/${goTarget.basename}`;
    const pack = packByName.get(packageName);
    if (!pack) {
        problem(`platform package ${packageName} was not packed`);        
        continue;
    }

    const before = problems.length;
    const pkg = readPackageJson(pack);
    if (pkg.version !== manifest.version) {
        problem(`${packageName} version ${pkg.version} != manifest ${manifest.version}`);
    }
    if (goTarget.type === "native" && 
        (JSON.stringify(pkg.os) !== JSON.stringify([goTarget.platform.nodejsPlatform]) || JSON.stringify(pkg.cpu) !== JSON.stringify([goTarget.platform.nodejsArch]))) {
        problem(`${packageName} os/cpu fields are wrong: ${JSON.stringify(pkg.os)}/${JSON.stringify(pkg.cpu)}`);
    }
    if (pkg.preferUnplugged !== true) {
        problem(`${packageName} must set preferUnplugged`);
    }
    if (!pkg.exports || pkg.exports["./package.json"] !== "./package.json") {
        problem(`${packageName} must export ./package.json`);
    }
    if (pkg.publishConfig?.access !== "public") {
        problem(`${packageName} must set publishConfig.access=public`);
    }

    const binTarget = goTarget.name === "wasi" ? "./bin/tsc-p-wasi" : `./${goTarget.executable}`;
    if (pkg.bin?.[goTarget.basename] !== binTarget) {
        problem(`${packageName} must declare bin.${goTarget.basename} = ${binTarget} (for standalone direct use)`);
    }

    const requires = ["package.json", "README.md", "LICENSE", "NOTICE.txt", goTarget.executable, ...(goTarget.name === "wasi" ? ["bin/tsc-p-wasi", "lib/run.mjs", "lib/main.mjs"] : [])];
    const entries = tarballEntries(pack.filename);
    for (const r of requires) {
        if (!entries.includes(r)) {
            problem(`${packageName} tarball missing ${r}`);
        }
    }
    const libCount = entries.filter(entry => entry.startsWith("lib/lib.") && entry.endsWith(".d.ts")).length;
    if (libCount !== expectedLibCount) {
        problem(`${packageName} tarball has ${libCount} standard-library files, expected ${expectedLibCount}`);
    }
    const allowed = goTarget.name === "wasi"
        ? new RegExp(`^(package\\.json|README\\.md|LICENSE|NOTICE\\.txt|bin/tsc-p-wasi|lib/(run|main)\\.mjs|lib/lib\\..*d\\.ts|${goTarget.executable.replace(/[.\\]/g, "\\$&")})$`)
        : new RegExp(`^(package\\.json|README\\.md|LICENSE|NOTICE\\.txt|lib/lib\\..*d\\.ts|${goTarget.executable.replace(/[.\\]/g, "\\$&")})$`);
    for (const entry of entries) {
        if (!allowed.test(entry)) {
            problem(`${packageName} tarball contains unexpected file: ${entry}`);
        }
    }

    if (problems.length === before) {
        ok(`${pack.filename}: version, platform/arch, bin, contents (${libCount} libs + ${goTarget.executable}) verified`);
    }
}

if (problems.length > 0) {
    console.error(`\n${problems.length} problem(s) found`);
    process.exit(1);
}
console.log("\nall package checks passed");
