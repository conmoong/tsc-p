// Assembles the npm package directories and tarballs from previously built binaries.
// Package directories land in built/tscp/npm/, tarballs in built/tscp/packages/ alongside an index file packs.json.

import fs from "node:fs";
import pathUtils from "node:path";
import {
    getManifest,
    fail,
    libsSourceDir,
    npmOutDir,
    packagesOutDir,
    repoRoot,
    runCapture,
    tscpDir,
} from "./lib.mjs";

const manifest = getManifest();

fs.rmSync(npmOutDir, { recursive: true, force: true });
fs.rmSync(packagesOutDir, { recursive: true, force: true });
fs.mkdirSync(npmOutDir, { recursive: true });
fs.mkdirSync(packagesOutDir, { recursive: true });

const libFiles = fs.readdirSync(libsSourceDir).filter(name => name.endsWith(".d.ts"));
if (libFiles.length === 0) {
    fail(`no standard-library files found in ${libsSourceDir}`);
}

function copyLicences(dir) {
    fs.copyFileSync(pathUtils.join(repoRoot, "LICENSE.txt"), pathUtils.join(dir, "LICENSE"));
    fs.copyFileSync(pathUtils.join(repoRoot, "NOTICE.txt"), pathUtils.join(dir, "NOTICE.txt"));
}

function writePackageJson(dir, contents) {
    fs.writeFileSync(pathUtils.join(dir, "package.json"), JSON.stringify(contents, undefined, 4) + "\n");
}

// --- platform packages -----------------------------------------------------

const dirsToPack = [];
for (const goTarget of manifest.goTargets) {
    if (!fs.existsSync(goTarget.executablePath)) {
        fail(`missing binary for ${goTarget.name}: ${goTarget.executablePath} (run npm run tscp:build first)`);        
    }

    const packageName = `@conmoong/${goTarget.basename}`;
    const dir = pathUtils.join(npmOutDir, goTarget.basename);
    fs.mkdirSync(pathUtils.join(dir, "lib"), { recursive: true });

    // The executable ships at the package root and it can also
    // be run directly and standalone via its own uniquely-named bin entry
    // below (e.g. `npx tsc-p-darwin-arm64`) for a user who already knows
    // their platform and wants to skip the root launcher's resolution
    // entirely — this is additive to, and independent of, how the root
    // launcher itself locates this same file (see getExePath.js).
    const exeDest = pathUtils.join(dir, goTarget.executable);
    fs.copyFileSync(goTarget.executablePath, exeDest);
    if (goTarget.type === "native") {
        // On Windows, the executable is a .exe file and is already executable.
        // On Unix, the executable is a plain file and needs +x permission to run.
        fs.chmodSync(exeDest, 0o755);
    }

    for (const lib of libFiles) {
        fs.copyFileSync(pathUtils.join(libsSourceDir, lib), pathUtils.join(dir, "lib", lib));
    }

    copyLicences(dir);

    if (goTarget.name === "wasi") {
        const wasiSource = pathUtils.join(tscpDir, "packages", "tsc-p-wasi");
        fs.mkdirSync(pathUtils.join(dir, "bin"), { recursive: true });
        fs.cpSync(pathUtils.join(wasiSource, "bin"), pathUtils.join(dir, "bin"), { recursive: true });
        fs.chmodSync(pathUtils.join(dir, "bin", "tsc-p-wasi"), 0o755);
        fs.cpSync(pathUtils.join(wasiSource, "lib"), pathUtils.join(dir, "lib"), { recursive: true });
        fs.copyFileSync(pathUtils.join(wasiSource, "README.md"), pathUtils.join(dir, "README.md"));
        const wasiPkg = JSON.parse(fs.readFileSync(pathUtils.join(wasiSource, "package.json"), "utf8"));
        delete wasiPkg.private;
        wasiPkg.version = manifest.version;
        writePackageJson(dir, wasiPkg);
    } else if (goTarget.type === "native") {
        fs.writeFileSync(
            pathUtils.join(dir, "README.md"),
            `# ${packageName}\n\nNative ${goTarget.platform.name} binary for [@conmoong/tsc-p](https://github.com/conmoong/tsc-p). `
                + `Can also be installed and run standalone: \`npx ${goTarget.basename}\`.\n\n`
                + `tsc-p is an unofficial downstream fork of microsoft/TypeScript's tsc/ native compiler and is not endorsed by Microsoft.`
        );
        writePackageJson(dir, {
            name: packageName,
            version: manifest.version,
            description: `Native ${goTarget.name} binary for @conmoong/tsc-p. Can be installed and run standalone.`,
            repository: { type: "git", url: "https://github.com/conmoong/tsc-p.git" },
            license: "Apache-2.0",
            os: [goTarget.platform.nodejsPlatform],
            cpu: [goTarget.platform.nodejsArch],
            bin: { [goTarget.basename]: `./${goTarget.executable}` },
            preferUnplugged: true,
            exports: { "./package.json": "./package.json" },
            files: [goTarget.executable, "lib", "NOTICE.txt"],
            publishConfig: { access: "public" },
        });
    }
    dirsToPack.push(dir);
}


// --- root launcher package ---------------------------------------------------

const launcherSource = pathUtils.join(tscpDir, "packages", "tsc-p");
const rootDir = pathUtils.join(npmOutDir, "tsc-p");
fs.mkdirSync(rootDir, { recursive: true });
fs.cpSync(pathUtils.join(launcherSource, "bin"), pathUtils.join(rootDir, "bin"), { recursive: true });
fs.cpSync(pathUtils.join(launcherSource, "lib"), pathUtils.join(rootDir, "lib"), { recursive: true });
fs.chmodSync(pathUtils.join(rootDir, "bin", "tsc-p"), 0o755);
copyLicences(rootDir);
fs.copyFileSync(pathUtils.join(launcherSource, "README.md"), pathUtils.join(rootDir, "README.md"));
const launcherPkg = JSON.parse(fs.readFileSync(pathUtils.join(launcherSource, "package.json"), "utf8"));
delete launcherPkg.private;
launcherPkg.version = manifest.version;
launcherPkg.optionalDependencies = {};
for (const goTarget of manifest.goTargets) {
    // Note: the root launcher only depends on native per-platform packages;
    // wasm targets are never platform-specific binaries npm can auto-select, 
    // so they are deliberately not listed in optionalDependencies. 
    // Users who want one must install it themselves.
    if (goTarget.type === "native") {
        launcherPkg.optionalDependencies[`@conmoong/${goTarget.basename}`] = manifest.version;
    }
}
writePackageJson(rootDir, launcherPkg);
dirsToPack.push(rootDir);

// --- npm pack ----------------------------------------------------------------

const packs = [];
for (const dir of dirsToPack) {
    const output = JSON.parse(runCapture("npm", ["pack", "--json", "--pack-destination", packagesOutDir], { cwd: dir }));
    const { filename, name, version, size } = output[0];
    packs.push({ name, version, filename, size, dir: pathUtils.relative(repoRoot, dir) });
    console.log(`packed ${filename}`);
}
fs.writeFileSync(pathUtils.join(packagesOutDir, "packs.json"), JSON.stringify(packs, undefined, 4) + "\n");
console.log(
    `packaged ${dirsToPack.length} package(s) into ${pathUtils.relative(repoRoot, packagesOutDir)}`,
);
