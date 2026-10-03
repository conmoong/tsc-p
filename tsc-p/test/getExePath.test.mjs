import { expect } from "chai";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import sinon from "sinon";
import getExePath, { UnsupportedPlatformError } from "../packages/tsc-p/lib/getExePath.js";

/**
 * Awaits an async function, asserting it rejects, and returns the error —
 * chai's `.to.throw()` only works for synchronous throws, and getExePath
 * is async.
 */
async function rejection(fn) {
    try {
        await fn();
    }
    catch (error) {
        return error;
    }
    expect.fail("expected the promise to reject");
}

describe("getExePath", () => {
    let tempDirs = [];

    function tempDir() {
        const dir = fs.mkdtempSync(path.join(os.tmpdir(), "tscp-test-"));
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
     * A fake launcher package.json, standing in for the real (hardcoded,
     * optionalDependencies-less) template — package.mjs reconstructs
     * optionalDependencies at build time, so getExePath's tests supply their
     * own via the `package` override instead of depending on the template.
     */
    function fakePackage(...supportedPackageNames) {
        const optionalDependencies = {};
        for (const name of supportedPackageNames) {
            optionalDependencies[name] = "0.0.0";
        }
        return { name: "@conmoong/tsc-p", optionalDependencies };
    }

    /**
     * Lays out a fake installed platform package and returns a resolve stub
     * plus paths.
     */
    function fakePlatformPackage(platform, arch, { createExe = true } = {}) {
        const dir = tempDir();
        const packageName = `@conmoong/tsc-p-${platform}-${arch}`;
        const packageDir = path.join(dir, "node_modules", ...packageName.split("/"));
        fs.mkdirSync(packageDir, { recursive: true });
        fs.writeFileSync(path.join(packageDir, "package.json"), JSON.stringify({ name: packageName }));
        const exeName = platform === "win32" ? `tsc-p-${platform}-${arch}.exe` : `tsc-p-${platform}-${arch}`;
        // The executable ships at the platform package's own root, not lib/
        // — see package.mjs's platform-package assembly.
        const exePath = path.join(packageDir, exeName);
        if (createExe) {
            fs.writeFileSync(exePath, "fake binary");
        }
        const resolve = sinon.stub()
            .withArgs(`${packageName}/package.json`)
            .returns(pathToFileURL(path.join(packageDir, "package.json")).href);
        return { resolve, exePath, packageName };
    }

    it("resolves the platform package executable through module resolution", async () => {
        const { resolve, exePath, packageName } = fakePlatformPackage("linux", "x64");
        const result = await getExePath({ platform: "linux", arch: "x64", env: {}, resolve, package: fakePackage(packageName) });
        expect(result).to.equal(exePath);
        expect(resolve.calledOnceWithExactly("@conmoong/tsc-p-linux-x64/package.json")).to.equal(true);
    });

    it("uses the .exe name on Windows", async () => {
        const { resolve, exePath, packageName } = fakePlatformPackage("win32", "arm64");
        const result = await getExePath({ platform: "win32", arch: "arm64", env: {}, resolve, package: fakePackage(packageName) });
        expect(result).to.equal(exePath);
        expect(result.endsWith("tsc-p-win32-arm64.exe")).to.equal(true);
    });

    it("throws a distinguishable UnsupportedPlatformError, mentioning the WASI fallback", async () => {
        const error = await rejection(() => getExePath({ platform: "freebsd", arch: "x64", env: {}, resolve: () => "unused", package: fakePackage("@conmoong/tsc-p-linux-x64") }));
        expect(error).to.be.instanceOf(UnsupportedPlatformError);
        expect(error.message).to.match(/does not support freebsd-x64.*Supported platforms.*Install @conmoong\/tsc-p-wasi/s);
    });

    it("throws a useful error when the platform package cannot be resolved", async () => {
        const resolve = sinon.stub().throws(new Error("Cannot find package"));
        const error = await rejection(() => getExePath({ platform: "linux", arch: "arm64", env: {}, resolve, package: fakePackage("@conmoong/tsc-p-linux-arm64") }));
        expect(error.message).to.match(/could not resolve its platform package @conmoong\/tsc-p-linux-arm64.*--omit=optional.*unsupported/s);
    });

    it("throws a useful error when the executable is missing from the package", async () => {
        const { resolve, packageName } = fakePlatformPackage("darwin", "arm64", { createExe: false });
        const error = await rejection(() => getExePath({ platform: "darwin", arch: "arm64", env: {}, resolve, package: fakePackage(packageName) }));
        expect(error.message).to.match(/executable is missing/);
    });

    it("honours TSC_P_EXE when it exists", async () => {
        const dir = tempDir();
        const fakeExe = path.join(dir, "fake-exe");
        fs.writeFileSync(fakeExe, "fake");
        const resolve = sinon.stub().throws(new Error("must not be called"));
        const result = await getExePath({ platform: "linux", arch: "x64", env: { TSC_P_EXE: fakeExe }, resolve });
        expect(result).to.equal(fakeExe);
        expect(resolve.called).to.equal(false);
    });

    it("rejects TSC_P_EXE pointing at a missing file", async () => {
        const error = await rejection(() => getExePath({ platform: "linux", arch: "x64", env: { TSC_P_EXE: "/does/not/exist" }, resolve: () => "unused" }));
        expect(error.message).to.match(/TSC_P_EXE is set but does not exist/);
    });
});

describe("launcher package.json", () => {
    const here = path.dirname(fileURLToPath(import.meta.url));
    const launcherPkg = JSON.parse(fs.readFileSync(path.join(here, "..", "packages", "tsc-p", "package.json"), "utf8"));

    // optionalDependencies is deliberately absent from the committed
    // template: package.mjs reconstructs it from manifest.json's target
    // matrix at build time (see package.mjs), so there is nothing to assert
    // here against the source template itself.

    it("declares the correct package name and required package fields", () => {
        expect(launcherPkg.name).to.equal("@conmoong/tsc-p");
        expect(launcherPkg.bin).to.deep.equal({ "tsc-p": "./bin/tsc-p" });
        expect(launcherPkg.preferUnplugged).to.equal(true);
        expect(launcherPkg.exports).to.have.property("./package.json");
        expect(launcherPkg.type).to.equal("module");
    });
});
