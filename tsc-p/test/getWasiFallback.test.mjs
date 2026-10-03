import { expect } from "chai";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { pathToFileURL } from "node:url";
import sinon from "sinon";
import getWasiFallback from "../packages/tsc-p/lib/getWasiFallback.js";

describe("getWasiFallback", () => {
    let tempDirs = [];

    function tempDir() {
        const dir = fs.mkdtempSync(path.join(os.tmpdir(), "tscp-wasi-test-"));
        tempDirs.push(dir);
        return dir;
    }

    afterEach(() => {
        for (const dir of tempDirs) {
            fs.rmSync(dir, { recursive: true, force: true });
        }
        tempDirs = [];
        sinon.restore();
    });

    /**
     * Lays out a fake installed @conmoong/tsc-p-wasi package and returns a
     * resolve stub handling both the package.json lookup and the bare
     * specifier import (its exports "." entry, lib/run.mjs).
     */
    function fakeWasiPackage({ createWasm = true, exportRunFn = true } = {}) {
        const dir = tempDir();
        const packageName = "@conmoong/tsc-p-wasi";
        const packageDir = path.join(dir, "node_modules", ...packageName.split("/"));
        fs.mkdirSync(path.join(packageDir, "lib"), { recursive: true });
        fs.writeFileSync(path.join(packageDir, "package.json"), JSON.stringify({ name: packageName }));
        if (createWasm) {
            // The compiled binary ships at the WASI package's own root, not
            // lib/ — see package.mjs's WASI-branch assembly and main.mjs.
            fs.writeFileSync(path.join(packageDir, "tsc-p-wasi.wasm"), "fake wasm bytes");
        }
        const runnerPath = path.join(packageDir, "lib", "run.mjs");
        fs.writeFileSync(
            runnerPath,
            exportRunFn ? "export async function run(wasmPath, args) { return { wasmPath, args }; }\n" : "export const notRun = 1;\n",
        );

        const resolve = sinon.stub();
        resolve.withArgs(`${packageName}/package.json`).returns(pathToFileURL(path.join(packageDir, "package.json")).href);
        resolve.withArgs(packageName).returns(pathToFileURL(runnerPath).href);
        return { resolve, packageDir, runnerPath };
    }

    it("resolves a callable, wasmPath-bound run() when the package is installed", async () => {
        const { resolve, packageDir } = fakeWasiPackage();
        const wasiRun = await getWasiFallback({ resolve });
        expect(wasiRun).to.be.a("function");
        const wasmPath = path.join(packageDir, "tsc-p-wasi.wasm");
        // The fake run.mjs (see fakeWasiPackage) echoes back its arguments,
        // proving wasmPath is already bound and only args needs passing.
        expect(await wasiRun(["--version"])).to.deep.equal({ wasmPath, args: ["--version"] });
    });

    it("returns null when the package cannot be resolved at all", async () => {
        const resolve = sinon.stub().throws(new Error("Cannot find package"));
        const result = await getWasiFallback({ resolve });
        expect(result).to.equal(null);
    });

    it("returns null when the package resolves but the wasm binary is missing", async () => {
        const { resolve } = fakeWasiPackage({ createWasm: false });
        const result = await getWasiFallback({ resolve });
        expect(result).to.equal(null);
    });
});
