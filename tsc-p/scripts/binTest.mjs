// Exercises a single tsc-p binary directly: check version, compiling a fixture with declarations and source maps, 
// and running the emitted JavaScript.
//
//   node tsc-p/scripts/binTest.mjs [--exe <path-to-binary>] [--project <dir>]

import assert from "node:assert";
import fs from "node:fs";
import os from "node:os";
import pathUtils from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { fail, runCapture, getManifest } from "./lib.mjs";

const manifest = getManifest();

const args = process.argv.slice(2); 

const projectIndex = args.indexOf("--project");
const persistentProject = projectIndex >= 0 ? pathUtils.resolve(args[projectIndex + 1]) : undefined;

const goTargets = [];
const exeIndex = args.indexOf("--exe");
if (exeIndex >= 0 && args[exeIndex + 1]) {       
    const executablePath = pathUtils.resolve(args[exeIndex + 1]);
    if (!fs.existsSync(executablePath)) {
        fail(`binary not found: ${executablePath}`);
    }
    goTargets.push({
        name: `Custom binary ${executablePath}`,
        executablePath,
    });
} else {
    if (manifest.currentPlatform == null || manifest.currentPlatform.nativeGoTargets.length + manifest.currentPlatform.wasmGoTargets.length === 0 ) {
        fail(`Current platform ${process.platform}/${process.arch} does not match any manifest platform`);
    }
    goTargets.push(...manifest.currentPlatform.nativeGoTargets);
    // Node's WASI implementation (uvwasi) has real, currently-unresolved
    // directory-enumeration bugs on Windows specifically (fd_readdir
    // reports "Function not implemented" and/or returns bad dirent data on
    // Windows — see nodejs/help#4231 and nodejs/uvwasi#148), which breaks
    // any glob-based tsconfig "include". This is a Node-level limitation
    // outside this repo's control, not something wrong in run.mjs. It's
    // also never a real user-facing path here: every current-platform
    // Windows target already ships a native binary, so getExePath.js never
    // falls through to the WASI fallback on Windows in practice.
    if (process.platform !== "win32") {
        goTargets.push(...manifest.currentPlatform.wasmGoTargets);
    }
}

function runTscp(target, args, options = {}) {
    if (target.name === "wasi") {
        // Spawns a fresh Node subprocess per invocation, exercising
        // run.mjs exactly as the packaged tsc-p-wasi binary would.
        const runnerUrl = pathToFileURL(pathUtils.join(pathUtils.dirname(fileURLToPath(import.meta.url)), "..", "packages", "tsc-p-wasi", "lib", "run.mjs")).href;
        const script = `
            const { run } = await import(${JSON.stringify(runnerUrl)});
            process.exitCode = await run(${JSON.stringify(target.executablePath)}, ${JSON.stringify(args)});
        `;
        return runCapture(process.execPath, ["--no-warnings", "--input-type=module", "-e", script], options).trim();
    } else {
        return runCapture(target.executablePath, args, options).trim();
    }
}

for (const goTarget of goTargets) {
    let projectDir;
    if (persistentProject) {
        projectDir = persistentProject;
    } else {
        projectDir = fs.mkdtempSync(pathUtils.join(os.tmpdir(), "tscp-bintest-"));
    }
    try {
        // Binaries that crossed an upload-artifact/download-artifact
        // round-trip (the CI "test" job downloads what the "build" job
        // produced) don't reliably keep the executable bit; a no-op on
        // Windows, where there is no such bit to lose.
        if (goTarget.name !== "wasi" && process.platform !== "win32") {
            fs.chmodSync(goTarget.executablePath, 0o755);
        }

        fs.mkdirSync(pathUtils.join(projectDir, "src"), { recursive: true });

        const version = runTscp(goTarget, ["--version"]);
        console.log(`--version -> ${version}`);
        assert.match(version, /^Version \d+\.\d+\.\d+/, "unexpected --version output");

        fs.writeFileSync(pathUtils.join(projectDir, "src", "greet.ts"), [
            `export interface Greeting {`,
            `    who: string;`,
            `}`,
            ``,
            `export function greet(g: Greeting): string {`,
            `    return \`G'day, \${g.who}!\`;`,
            `}`,
            ``,
        ].join("\n"));
        fs.writeFileSync(pathUtils.join(projectDir, "src", "main.ts"), [
            `import { greet } from "./greet.js";`,
            ``,
            `console.log(greet({ who: "bintest" }));`,
            ``,
        ].join("\n"));
        fs.writeFileSync(pathUtils.join(projectDir, "tsconfig.json"), JSON.stringify({
            compilerOptions: {
                module: "nodenext",
                target: "es2022",
                rootDir: "src",
                outDir: "dist",
                declaration: true,
                declarationMap: true,
                sourceMap: true,
                strict: true,
            },
            include: ["src"],
        }, undefined, 4));

        fs.rmSync(pathUtils.join(projectDir, "dist"), { recursive: true, force: true });
        runTscp(goTarget, ["-p", "."], { cwd: projectDir });

        for (const expected of ["main.js", "main.js.map", "main.d.ts", "main.d.ts.map", "greet.js", "greet.d.ts", "greet.d.ts.map"]) {
            assert.ok(fs.existsSync(pathUtils.join(projectDir, "dist", expected)), `missing dist/${expected}`);
        }

        const output = runCapture(process.execPath, [pathUtils.join(projectDir, "dist", "main.js")], { cwd: projectDir }).trim();
        console.log(`node dist/main.js -> ${output}`);
        assert.equal(output, "G'day, bintest!");

        const map = JSON.parse(fs.readFileSync(pathUtils.join(projectDir, "dist", "main.js.map"), "utf8"));
        assert.deepEqual(map.sources, ["../src/main.ts"], "js map sources wrong");

        console.log(`bintest passed for ${goTarget.executablePath} (TypeScript ${manifest.typescriptVersion} expected: ${version.includes(manifest.typescriptVersion) ? "matched" : "DIFFERENT — upstream drift"})`);
    }
    finally {
        if (persistentProject == null) {
            fs.rmSync(projectDir, { recursive: true, force: true });
        }
    }
}

