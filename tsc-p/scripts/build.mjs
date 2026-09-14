// Builds the tsc-p native binaries (cross-compile every target including WASM). 
// Binaries land in built/tscp/bin/tsc-p-<target_name> (optionally with .exe on Windows)
// Builds are reproducible: CGO_ENABLED=0, -trimpath, and no timestamps are embedded.

import fs from "node:fs";
import pathUtils from "node:path";
import { getManifest, goDir, binOutDir, fail, run } from "./lib.mjs";

const manifest = getManifest();

const goMod = fs.readFileSync(pathUtils.join(goDir, "go.mod"), "utf8");
const match = goMod.match(/^go\s+(\S+)$/m);
if (!match) {
    fail("could not find a go directive in go.mod");
}
if (match[1] !== manifest.goVersion) {
    fail(`manifest.json goVersion (${manifest.goVersion}) does not match go.mod (${match[1]}); update tsc-p/manifest.json`);
}

for (const goTarget of manifest.goTargets) {
    fs.mkdirSync(binOutDir, { recursive: true });
    run("go", [
        "build",
        "-trimpath",
        "-ldflags=-s -w",
        "-o",
        goTarget.executablePath,
        "./cmd/tsc",
    ], {
        cwd: goDir,
        env: {
            ...process.env,
            CGO_ENABLED: "0",
            GOOS: goTarget.goOs,
            GOARCH: goTarget.goArch,
        },
    });
    console.log(`built ${goTarget.executablePath}`);
}
