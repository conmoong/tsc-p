// End-to-end test of the automatic WASI fallback: with the launcher's own
// package.json edited to declare NO native targets (simulating every
// platform being "unsupported"), and a real @conmoong/tsc-p-wasi package
// installed alongside it, bin/tsc-p must transparently fall back to
// running the compiler via node:wasi and compile a real fixture.
//
// Requires `node tsc-p/scripts/build.mjs && node tsc-p/scripts/package.mjs`
// to have been run first; skips itself otherwise (same convention as
// smoke.mjs's prerequisite check, but as a skip rather than a hard fail
// since this suite also runs in plain `npm test`).

import { expect } from "chai";
import { spawnSync } from "node:child_process";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));
const repoRoot = path.join(here, "..", "..");
const launcherSource = path.join(here, "..", "packages", "tsc-p");
const wasiPackageBuilt = path.join(repoRoot, "built", "tscp", "npm", "tsc-p-wasi");

describe("launcher WASI fallback (end-to-end)", function () {
    this.timeout(30_000);

    let tempDirs = [];

    function tempDir() {
        const dir = fs.mkdtempSync(path.join(os.tmpdir(), "tscp-wasi-e2e-"));
        tempDirs.push(dir);
        return dir;
    }

    afterEach(() => {
        for (const dir of tempDirs) {
            fs.rmSync(dir, { recursive: true, force: true });
        }
        tempDirs = [];
    });

    before(function () {
        if (!fs.existsSync(wasiPackageBuilt)) {
            this.skip();
        }
    });

    /**
     * Assembles a standalone copy of the launcher whose package.json
     * declares no native targets at all (so getExePath always throws
     * UnsupportedPlatformError, regardless of the real host platform),
     * with the real built @conmoong/tsc-p-wasi package installed
     * alongside it under node_modules.
     */
    function setupFallbackProject() {
        const project = tempDir();
        fs.cpSync(path.join(launcherSource, "bin"), path.join(project, "bin"), { recursive: true });
        fs.cpSync(path.join(launcherSource, "lib"), path.join(project, "lib"), { recursive: true });
        fs.chmodSync(path.join(project, "bin", "tsc-p"), 0o755);

        const pkg = JSON.parse(fs.readFileSync(path.join(launcherSource, "package.json"), "utf8"));
        pkg.optionalDependencies = {}; // no native target ever matches -> UnsupportedPlatformError
        fs.writeFileSync(path.join(project, "package.json"), JSON.stringify(pkg, undefined, 4));

        const wasiDest = path.join(project, "node_modules", "@conmoong", "tsc-p-wasi");
        fs.mkdirSync(path.dirname(wasiDest), { recursive: true });
        fs.cpSync(wasiPackageBuilt, wasiDest, { recursive: true });

        return project;
    }

    it("falls back to the WASI package and compiles a real fixture", () => {
        const project = setupFallbackProject();
        fs.mkdirSync(path.join(project, "src"), { recursive: true });
        fs.writeFileSync(path.join(project, "src", "index.ts"), "export function greet(name: string): string { return \"hi \" + name; }\n");
        fs.writeFileSync(path.join(project, "tsconfig.json"), JSON.stringify({
            compilerOptions: { target: "es2022", module: "esnext", outDir: "out", rootDir: "src", declaration: true },
            include: ["src"],
        }));

        const result = spawnSync(process.execPath, ["--no-warnings", path.join(project, "bin", "tsc-p"), "-p", "tsconfig.json"], {
            cwd: project,
            encoding: "utf8",
        });

        expect(result.status, `stdout: ${result.stdout}\nstderr: ${result.stderr}`).to.equal(0);
        expect(fs.readFileSync(path.join(project, "out", "index.js"), "utf8")).to.contain("function greet(name)");
        expect(fs.readFileSync(path.join(project, "out", "index.d.ts"), "utf8")).to.contain("declare function greet(name: string): string;");
    });

    it("propagates a non-zero exit code for a real type error through the fallback", () => {
        const project = setupFallbackProject();
        fs.mkdirSync(path.join(project, "src"), { recursive: true });
        fs.writeFileSync(path.join(project, "src", "index.ts"), "export const bad: number = \"not a number\";\n");
        fs.writeFileSync(path.join(project, "tsconfig.json"), JSON.stringify({
            compilerOptions: { target: "es2022", module: "esnext", outDir: "out", rootDir: "src" },
            include: ["src"],
        }));

        const result = spawnSync(process.execPath, ["--no-warnings", path.join(project, "bin", "tsc-p"), "-p", "tsconfig.json"], {
            cwd: project,
            encoding: "utf8",
        });

        expect(result.status).to.be.greaterThan(0);
        expect(result.stdout).to.contain("TS2322");
    });

    it("prints the unsupported-platform error (mentioning the WASI package) when it is NOT installed", () => {
        const project = tempDir();
        fs.cpSync(path.join(launcherSource, "bin"), path.join(project, "bin"), { recursive: true });
        fs.cpSync(path.join(launcherSource, "lib"), path.join(project, "lib"), { recursive: true });
        fs.chmodSync(path.join(project, "bin", "tsc-p"), 0o755);
        const pkg = JSON.parse(fs.readFileSync(path.join(launcherSource, "package.json"), "utf8"));
        pkg.optionalDependencies = {};
        fs.writeFileSync(path.join(project, "package.json"), JSON.stringify(pkg, undefined, 4));

        const result = spawnSync(process.execPath, [path.join(project, "bin", "tsc-p"), "--version"], {
            cwd: project,
            encoding: "utf8",
        });

        expect(result.status).to.equal(1);
        expect(result.stderr).to.contain("does not support");
        expect(result.stderr).to.contain("@conmoong/tsc-p-wasi");
    });
});
