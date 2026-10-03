import { expect } from "chai";
import path from "node:path";
import { localBinPath } from "../scripts/lib.mjs";

// Regression test for a Windows CI failure: an absolute path through a
// directory containing a space (e.g. the smoke test's own "proj with
// spaces" fixture, or a real user's Windows profile directory), passed as
// the command to execFileSync with shell:true, gets silently mis-tokenized
// by cmd.exe because Node's shell:true does not quote file/args for you —
// it just joins them with spaces. localBinPath must stay relative so
// callers combine it with `cwd` instead of an absolute path.
describe("localBinPath", () => {
    it("is relative to node_modules/.bin, not an absolute path", () => {
        const result = localBinPath("tsc-p", "linux");
        expect(path.isAbsolute(result)).to.equal(false);
        expect(result).to.equal(path.join("node_modules", ".bin", "tsc-p"));
    });

    it("appends .cmd on win32", () => {
        expect(localBinPath("tsc-p", "win32")).to.equal(path.join("node_modules", ".bin", "tsc-p.cmd"));
    });

    it("does not append .cmd on non-Windows platforms", () => {
        expect(localBinPath("tsc-p", "darwin")).to.equal(path.join("node_modules", ".bin", "tsc-p"));
        expect(localBinPath("tsc-p", "linux")).to.equal(path.join("node_modules", ".bin", "tsc-p"));
    });

    it("defaults to the real process.platform when not overridden", () => {
        const expected = path.join("node_modules", ".bin", process.platform === "win32" ? "tsc-p.cmd" : "tsc-p");
        expect(localBinPath("tsc-p")).to.equal(expected);
    });
});
