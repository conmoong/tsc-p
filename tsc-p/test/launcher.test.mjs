// End-to-end tests of bin/tsc-p as a child process: argument fidelity,
// exit-code propagation and stdio inheritance. The native binary is
// replaced with Node itself via TSC_P_EXE, and the first argument is a
// script that records what it received.

import { expect } from "chai";
import { spawnSync } from "node:child_process";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));
const launcherBin = path.join(here, "..", "packages", "tsc-p", "bin", "tsc-p");

describe("launcher execution", function () {
    this.timeout(20_000);

    let tempDirs = [];

    function tempDir() {
        const dir = fs.mkdtempSync(path.join(os.tmpdir(), "tscp-exec-"));
        tempDirs.push(dir);
        return dir;
    }

    afterEach(() => {
        for (const dir of tempDirs) {
            fs.rmSync(dir, { recursive: true, force: true });
        }
        tempDirs = [];
    });

    /**
     * Runs bin/tsc-p with TSC_P_EXE=node and a recorder script as the first
     * argument. The recorder writes its argv to a file and exits with the
     * requested code.
     */
    function runLauncher(userArgs, { exitCode = 0 } = {}) {
        const dir = tempDir();
        const argvFile = path.join(dir, "argv.json");
        const recorder = path.join(dir, "recorder.mjs");
        fs.writeFileSync(recorder, [
            `import fs from "node:fs";`,
            `fs.writeFileSync(${JSON.stringify(argvFile)}, JSON.stringify(process.argv.slice(2)));`,
            `console.log("recorder-stdout");`,
            `console.error("recorder-stderr");`,
            `process.exit(${exitCode});`,
        ].join("\n"));

        const result = spawnSync(process.execPath, [launcherBin, recorder, ...userArgs], {
            env: { ...process.env, TSC_P_EXE: process.execPath },
            encoding: "utf8",
        });
        const recorded = fs.existsSync(argvFile) ? JSON.parse(fs.readFileSync(argvFile, "utf8")) : null;
        return { result, recorded };
    }

    it("passes every argument exactly once and unchanged", () => {
        const args = ["--project", "tsconfig.json", "--outDir", "dist"];
        const { result, recorded } = runLauncher(args);
        expect(result.status).to.equal(0);
        expect(recorded).to.deep.equal(args);
    });

    it("preserves arguments containing spaces and unusual characters", () => {
        const args = ["path with spaces/tsconfig.json", "--outDir", "out dir/sub", "a=$b;c&d", "ünïcode"];
        const { recorded } = runLauncher(args);
        expect(recorded).to.deep.equal(args);
    });

    it("propagates the child exit code", () => {
        const { result } = runLauncher(["anything"], { exitCode: 3 });
        expect(result.status).to.equal(3);
    });

    it("propagates a zero exit code", () => {
        const { result } = runLauncher([], { exitCode: 0 });
        expect(result.status).to.equal(0);
    });

    it("inherits stdout and stderr", () => {
        const { result } = runLauncher([]);
        expect(result.stdout).to.contain("recorder-stdout");
        expect(result.stderr).to.contain("recorder-stderr");
    });

    it("fails with the resolver error when TSC_P_EXE is invalid", () => {
        const result = spawnSync(process.execPath, [launcherBin, "--version"], {
            env: { ...process.env, TSC_P_EXE: "/does/not/exist" },
            encoding: "utf8",
        });
        expect(result.status).to.equal(1);
        expect(result.stderr).to.contain("TSC_P_EXE is set but does not exist");
    });
});
