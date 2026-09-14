import { expect } from "chai";
import { needsWindowsShell } from "../scripts/lib.mjs";

// npm/npx, and any package's bin entry once npm installs it on Windows
// (e.g. node_modules/.bin/tsc-p.cmd), are .cmd/.bat shim scripts. Node
// refuses to spawn a .bat/.cmd file directly (with or without an explicit
// extension) unless the shell option is set — this is deliberate hardening
// from the fix for CVE-2024-27980. Routing the call through a shell lets
// cmd.exe apply its own resolution. See tsc-p/scripts/lib.mjs for the full
// explanation.
describe("needsWindowsShell", () => {
    it("is true for npm and npx on win32", () => {
        expect(needsWindowsShell("npm", "win32")).to.equal(true);
        expect(needsWindowsShell("npx", "win32")).to.equal(true);
    });

    it("is true for any .cmd or .bat path on win32", () => {
        expect(needsWindowsShell("C:\\proj\\node_modules\\.bin\\tsc-p.cmd", "win32")).to.equal(true);
        expect(needsWindowsShell("C:\\Users\\RUNNER~1\\AppData\\Local\\Temp\\proj with spaces\\node_modules\\.bin\\tsc-p.cmd", "win32")).to.equal(true);
        expect(needsWindowsShell("build.bat", "win32")).to.equal(true);
        expect(needsWindowsShell("BUILD.CMD", "win32")).to.equal(true);
    });

    it("is false for npm, npx, and .cmd/.bat paths on non-Windows platforms", () => {
        expect(needsWindowsShell("npm", "darwin")).to.equal(false);
        expect(needsWindowsShell("npm", "linux")).to.equal(false);
        expect(needsWindowsShell("npx", "linux")).to.equal(false);
        expect(needsWindowsShell("/tmp/proj/node_modules/.bin/tsc-p.cmd", "darwin")).to.equal(false);
    });

    it("is false for unrelated commands and native-executable full paths even on win32", () => {
        expect(needsWindowsShell("go", "win32")).to.equal(false);
        expect(needsWindowsShell("tar", "win32")).to.equal(false);
        expect(needsWindowsShell("C:\\proj\\built\\tscp\\bin\\win32-x64\\tsc-p.exe", "win32")).to.equal(false);
        expect(needsWindowsShell("C:\\proj\\node_modules\\@conmoong\\tsc-p-win32-x64\\lib\\tsc-p.exe", "win32")).to.equal(false);
    });

    it("defaults to the real process.platform when not overridden", () => {
        const expected = process.platform === "win32";
        expect(needsWindowsShell("npm")).to.equal(expected);
    });
});
