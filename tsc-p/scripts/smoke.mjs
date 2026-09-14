// Installs the packed root and packages that are compaitble with current platform into a clean
// temporary project, then verifies the  full user experience: tsc-p --version, 
// compiling a fixture, running the emitted JavaScript, and checking declarations and source maps.
//

import assert from "node:assert";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { fail, packagesOutDir, getManifest, runCapture, localBinPath, tscpDir } from "./lib.mjs";

const manifest = getManifest();

if (manifest.currentPlatform == null || manifest.currentPlatform.nativeGoTargets.length === 0) {
    fail(`Current platform ${process.platform}/${process.arch} does not match any manifest platform`);
}

const packs = JSON.parse(fs.readFileSync(path.join(packagesOutDir, "packs.json"), "utf8"));
const rootPack = packs.find(p => p.name === '@conmoong/tsc-p');
if (!rootPack) {
    fail("launcher tarball missing; run package.mjs first");
}
const wasiPack = packs.find(p => p.name === `@conmoong/tsc-p-wasi`);
if (!wasiPack) {
    fail("WASI tarball missing; run package.mjs first");
}

for (const goTarget of manifest.currentPlatform.nativeGoTargets) {    
    const platformPack = packs.find(p => p.name === `@conmoong/${goTarget.basename}`);        
    if (!platformPack) {
        fail("current-platform tarball missing; run package.mjs first");
    }

    const base = fs.mkdtempSync(path.join(os.tmpdir(), "tscp-smoke-"));
    const project = path.join(base, "proj with spaces");
    fs.mkdirSync(path.join(project, "src"), { recursive: true });

    try {
        fs.writeFileSync(path.join(project, "package.json"), JSON.stringify({ name: "smoke", private: true, type: "module" }));

        console.log(`installing tarballs into ${project}`);
        runCapture("npm", [
            "install",
            "--no-audit",
            "--no-fund",
            path.join(packagesOutDir, rootPack.filename),
            path.join(packagesOutDir, platformPack.filename),
        ], { cwd: project });

        // Deliberately relative to `cwd: project`, not an absolute path through
        // it — see localBinPath's doc comment for why (this project's own
        // fixture directory contains a space).
        const binary = localBinPath("tsc-p");

        // 1. --version reports the upstream TypeScript version.
        const version = runCapture(binary, ["--version"], { cwd: project }).trim();
        console.log(`tsc-p --version -> ${version}`);
        assert.match(version, new RegExp(manifest.typescriptVersion.replace(/\./g, "\\.")), "unexpected --version output");

        // 2. Compile a fixture with declarations and source maps.
        fs.writeFileSync(path.join(project, "src", "greet.ts"), [
            `export interface Greeting {`,
            `    who: string;`,
            `}`,
            ``,
            `export function greet(g: Greeting): string {`,
            `    return \`G'day, \${g.who}!\`;`,
            `}`,
            ``,
        ].join("\n"));
        fs.writeFileSync(path.join(project, "src", "main.ts"), [
            `import { greet } from "./greet.js";`,
            ``,
            `console.log(greet({ who: "tsc-p smoke" }));`,
            ``,
        ].join("\n"));
        fs.writeFileSync(path.join(project, "tsconfig.json"), JSON.stringify({
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

        runCapture(binary, ["-p", "."], { cwd: project });

        for (const expected of ["main.js", "main.js.map", "main.d.ts", "main.d.ts.map", "greet.js", "greet.d.ts"]) {
            assert.ok(fs.existsSync(path.join(project, "dist", expected)), `missing dist/${expected}`);
        }

        // 3. The emitted JavaScript runs.
        const output = runCapture(process.execPath, [path.join(project, "dist", "main.js")], { cwd: project }).trim();
        console.log(`node dist/main.js -> ${output}`);
        assert.equal(output, "G'day, tsc-p smoke!");

        // 4. Source maps reference the original sources.
        const map = JSON.parse(fs.readFileSync(path.join(project, "dist", "main.js.map"), "utf8"));
        assert.deepEqual(map.sources, ["../src/main.ts"], "js map sources wrong");
        const dtsMap = JSON.parse(fs.readFileSync(path.join(project, "dist", "main.d.ts.map"), "utf8"));
        assert.deepEqual(dtsMap.sources, ["../src/main.ts"], "dts map sources wrong");

        // 5. Declarations contain the expected surface.
        const dts = fs.readFileSync(path.join(project, "dist", "greet.d.ts"), "utf8");
        assert.match(dts, /export declare function greet\(g: Greeting\): string;/);

        // 6. Compile errors propagate a non-zero exit code.
        fs.writeFileSync(path.join(project, "src", "broken.ts"), `export const bad: number = "not a number";\n`);
        let failed = false;
        try {
            runCapture(binary, ["-p", "."], { cwd: project });
        }
        catch (error) {
            failed = true;
            assert.ok(error.status > 0, "expected a positive exit status for a type error");
        }
        assert.ok(failed, "expected the broken fixture to fail");

        // 7. Path rewriting: with an "@conmoong/path-rewrite" entry in
        // compilerOptions.plugins, tsconfig path aliases are rewritten to
        // relative paths with extensions derived from the resolved file
        // (.ts -> .js, .mts -> .mjs, .cts -> .cjs, .json kept), and the
        // emitted JavaScript actually runs. Without the entry, aliases stay
        // exactly as written.
        //
        // The project is nested inside the installed project (whose own path
        // contains a space) so the relative bin shim still resolves, and its
        // directory name is space-free so it can be passed as a -p argument
        // (Windows shell:true joins arguments without quoting).
        const rewriteProject = path.join(project, "rewriteproj");
        fs.mkdirSync(path.join(rewriteProject, "src", "lib"), { recursive: true });
        fs.mkdirSync(path.join(rewriteProject, "src", "data"), { recursive: true });
        fs.writeFileSync(path.join(rewriteProject, "package.json"), JSON.stringify({ name: "rewrite-smoke", private: true, type: "module" }));
        fs.writeFileSync(path.join(rewriteProject, "tsconfig.off.json"), JSON.stringify({
            extends: "./tsconfig.json",
            compilerOptions: { outDir: "dist-off", declaration: false, sourceMap: false, plugins: [] },
        }, undefined, 4));
        fs.writeFileSync(path.join(rewriteProject, "tsconfig.json"), JSON.stringify({
            compilerOptions: {
                module: "nodenext",
                target: "es2022",
                rootDir: "src",
                outDir: "dist",
                declaration: true,
                sourceMap: true,
                strict: true,
                resolveJsonModule: true,
                plugins: [
                    { name: "@conmoong/path-rewrite" },
                ],
                paths: {
                    "@lib/*": ["./src/lib/*"],
                    "@gamma": ["./src/lib/gamma.mts"],
                    "@delta": ["./src/lib/delta.cts"],
                    "@config": ["./src/data/config.json"],
                },
            },
            include: ["src"],
        }, undefined, 4));
        fs.writeFileSync(path.join(rewriteProject, "src", "lib", "alpha.ts"), "export const alpha = 10;\nexport type AlphaType = number;\n");
        fs.writeFileSync(path.join(rewriteProject, "src", "lib", "gamma.mts"), "export default 20;\n");
        fs.writeFileSync(path.join(rewriteProject, "src", "lib", "delta.cts"), "const value = 30;\nexport = value;\n");
        fs.writeFileSync(path.join(rewriteProject, "src", "data", "config.json"), `{ "count": 40 }\n`);
        fs.writeFileSync(path.join(rewriteProject, "src", "index.ts"), [
            `import { alpha, type AlphaType } from "@lib/alpha.js";`,
            `import gammaValue from "@gamma";`,
            `import deltaValue from "@delta";`,
            `import config from "@config" with { type: "json" };`,
            ``,
            `export const total: AlphaType = alpha + gammaValue + deltaValue + config.count;`,
            `console.log(\`total=\${total}\`);`,
            ``,
            `export function loadGamma() {`,
            `    return import("@gamma");`,
            `}`,
            ``,
        ].join("\n"));

        // Without the plugins entry (tsconfig.off.json overrides it away),
        // aliases must stay as written.
        runCapture(binary, ["-p", "rewriteproj/tsconfig.off.json"], { cwd: project });
        const offJs = fs.readFileSync(path.join(rewriteProject, "dist-off", "index.js"), "utf8");
        assert.ok(offJs.includes(`from "@gamma"`), "without the plugins entry, aliases must stay as written");

        // With it, every alias is rewritten and the output runs.
        runCapture(binary, ["-p", "rewriteproj"], { cwd: project });
        const rewrittenJs = fs.readFileSync(path.join(rewriteProject, "dist", "index.js"), "utf8");
        for (const expected of [`from "./lib/alpha.js"`, `from "./lib/gamma.mjs"`, `from "./lib/delta.cjs"`, `from "./data/config.json"`, `import("./lib/gamma.mjs")`]) {
            assert.ok(rewrittenJs.includes(expected), `rewritten output missing ${expected}:\n${rewrittenJs}`);
        }
        assert.ok(!rewrittenJs.includes(`"@`), `an alias leaked into the rewritten output:\n${rewrittenJs}`);
        const rewrittenDts = fs.readFileSync(path.join(rewriteProject, "dist", "index.d.ts"), "utf8");
        assert.ok(rewrittenDts.includes(`from "./lib/alpha.js"`), `declaration output not rewritten:\n${rewrittenDts}`);
        assert.ok(rewrittenDts.includes(`import("./lib/gamma.mjs")`), `declaration import type not rewritten:\n${rewrittenDts}`);

        const rewriteOutput = runCapture(process.execPath, [path.join(rewriteProject, "dist", "index.js")], { cwd: rewriteProject }).trim();
        console.log(`path-rewrite: node dist/index.js -> ${rewriteOutput}`);
        assert.equal(rewriteOutput, "total=100", "rewritten output did not run correctly");

        // 8. Bouncer: with an "@conmoong/bouncer" entry in
        // compilerOptions.plugins, @bouncer-tagged declarations are removed or
        // have their export modifier toggled, in both JavaScript and
        // declaration output, and the emitted JavaScript still runs correctly.
        const bouncerProject = path.join(project, "bouncerproj");
        fs.mkdirSync(path.join(bouncerProject, "src"), { recursive: true });
        fs.writeFileSync(path.join(bouncerProject, "package.json"), JSON.stringify({ name: "bouncer-smoke", private: true, type: "module" }));
        fs.writeFileSync(path.join(bouncerProject, "tsconfig.json"), JSON.stringify({
            compilerOptions: {
                module: "nodenext",
                target: "es2022",
                rootDir: "src",
                outDir: "dist",
                declaration: true,
                strict: true,
                plugins: [
                    { name: "@conmoong/bouncer", release: "beta", profiles: { "prod-hide": "no-export" } },
                ],
            },
            include: ["src"],
        }, undefined, 4));
        fs.writeFileSync(path.join(bouncerProject, "src", "index.ts"), [
            `/**`,
            ` * @bouncer remove`,
            ` */`,
            `export function mockNetworkCall(): Promise<number> {`,
            `    return Promise.resolve(42);`,
            `}`,
            ``,
            `/**`,
            ` * @bouncer profile prod-hide`,
            ` */`,
            `export function debugDump(x: unknown): void {`,
            `    console.log(x);`,
            `}`,
            ``,
            `/**`,
            ` * @bouncer export default`,
            ` */`,
            `export function add(a: number, b: number): number {`,
            `    return a + b;`,
            `}`,
            ``,
            `/**`,
            ` * @bouncer export sum`,
            ` */`,
            `function total(a: number, b: number): number {`,
            `    return add(a, b);`,
            `}`,
            ``,
            `/**`,
            ` * @bouncer release internal`,
            ` */`,
            `export function internalOnly(): number {`,
            `    return 7;`,
            `}`,
            ``,
            `console.log(\`total(2,3)=\${total(2, 3)}\`);`,
            ``,
        ].join("\n"));

        runCapture(binary, ["-p", "bouncerproj"], { cwd: project });
        const bouncerJs = fs.readFileSync(path.join(bouncerProject, "dist", "index.js"), "utf8");
        const bouncerDts = fs.readFileSync(path.join(bouncerProject, "dist", "index.d.ts"), "utf8");
        assert.ok(!bouncerJs.includes("mockNetworkCall"), `removed declaration leaked into JS:\n${bouncerJs}`);
        assert.ok(!bouncerDts.includes("mockNetworkCall"), `removed declaration leaked into declarations:\n${bouncerDts}`);
        assert.ok(!bouncerJs.includes("export function debugDump"), `profile no-export not applied in JS:\n${bouncerJs}`);
        assert.ok(!bouncerDts.includes("debugDump"), `profile no-export did not omit the declaration from declarations:\n${bouncerDts}`);
        assert.ok(!bouncerJs.includes("export function add"), `export default did not strip the original export in JS:\n${bouncerJs}`);
        assert.ok(bouncerJs.includes("export default add;"), `export default statement missing in JS:\n${bouncerJs}`);
        assert.ok(bouncerJs.includes("export { total as sum };"), `export-as statement missing in JS:\n${bouncerJs}`);
        assert.ok(bouncerJs.includes("export function internalOnly"), `release channel must never affect JS output:\n${bouncerJs}`);
        assert.ok(!bouncerDts.includes("internalOnly"), `release: "beta" must trim an "internal" declaration from .d.ts:\n${bouncerDts}`);

        const bouncerOutput = runCapture(process.execPath, [path.join(bouncerProject, "dist", "index.js")], { cwd: bouncerProject }).trim();
        console.log(`bouncer: node dist/index.js -> ${bouncerOutput}`);
        assert.equal(bouncerOutput, "total(2,3)=5", "bouncer-processed output did not run correctly");

        // 9. Paris: with an "@conmoong/paris" entry in compilerOptions.plugins,
        // conditions are frozen at build time (true bodies become blocks, false
        // branches vanish with inner conditions unevaluated), value() calls
        // become literals from every source kind, and the runtime import is
        // elided so the emitted output runs with no @conmoong/paris dependency.
        // Misuse must fail the build with a TS990101 diagnostic.
        const parisProject = path.join(project, "parisproj");
        fs.mkdirSync(path.join(parisProject, "src"), { recursive: true });
        fs.writeFileSync(path.join(parisProject, "package.json"), JSON.stringify({ name: "paris-smoke", private: true, type: "module" }));
        // The real published runtime package (types included) so the import
        // resolves and type-checks; the plugin then elides it from the
        // compiled output. @conmoong/paris is a separate package (its own
        // repository), not part of this build, so it comes from the
        // registry rather than a local copy.
        runCapture("npm", ["install", "--no-audit", "--no-fund", "@conmoong/paris"], { cwd: parisProject });
        fs.writeFileSync(path.join(parisProject, "VERSION.txt"), "9.9.9");
        fs.writeFileSync(path.join(parisProject, ".env"), "FEATURE_X=on\n");
        fs.writeFileSync(path.join(parisProject, "tsconfig.json"), JSON.stringify({
            compilerOptions: {
                module: "nodenext",
                moduleResolution: "nodenext",
                target: "es2022",
                strict: true,
                outDir: "dist",
                rootDir: "src",
                plugins: [{
                    name: "@conmoong/paris",
                    define: {
                        MODE: "prod",
                        LEVEL: { value: 5 },
                        VERSION: { from: "file", path: "VERSION.txt", type: "string" },
                        FEATURE_X: { from: "dot_env" },
                    },
                }],
            },
            include: ["src"],
        }));
        fs.writeFileSync(path.join(parisProject, "src", "index.ts"), [
            `import { ifDef, ifEq, ifGt, ifTruthy, value } from "@conmoong/paris";`,
            ``,
            `console.log("version=" + value("VERSION"));`,
            `ifEq("MODE", "prod", () => {`,
            `    ifGt("LEVEL", 3, () => {`,
            `        console.log("level>3");`,
            `    });`,
            `});`,
            `ifTruthy("FEATURE_X", () => {`,
            `    console.log("featureX=on");`,
            `});`,
            `ifDef("ABSENT", () => {`,
            `    console.log("should-not-appear");`,
            `});`,
            ``,
        ].join("\n"));

        runCapture(binary, ["-p", "parisproj"], { cwd: project });
        const parisJs = fs.readFileSync(path.join(parisProject, "dist", "index.js"), "utf8");
        assert.ok(parisJs.includes(`"version=" + "9.9.9"`), `value() was not inlined from the file source:\n${parisJs}`);
        assert.ok(!parisJs.includes("should-not-appear"), `false branch survived:\n${parisJs}`);
        assert.ok(!parisJs.includes("@conmoong/paris"), `runtime import was not elided:\n${parisJs}`);
        assert.ok(!parisJs.includes("ifEq"), `condition call survived:\n${parisJs}`);

        const parisOutput = runCapture(process.execPath, [path.join(parisProject, "dist", "index.js")], { cwd: parisProject }).trim();
        console.log(`paris: node dist/index.js -> ${parisOutput.replaceAll("\n", " | ")}`);
        assert.equal(parisOutput, "version=9.9.9\nlevel>3\nfeatureX=on", "paris-processed output did not run correctly");

        // Misuse fails the build loudly (return inside a condition body).
        fs.writeFileSync(path.join(parisProject, "src", "bad.ts"), [
            `import { ifDef } from "@conmoong/paris";`,
            `export function f(): void {`,
            `    ifDef("MODE", () => {`,
            `        return;`,
            `    });`,
            `}`,
            ``,
        ].join("\n"));
        let parisFailed = false;
        try {
            runCapture(binary, ["-p", "parisproj"], { cwd: project });
        }
        catch (error) {
            parisFailed = true;
            // execFileSync attaches the child's captured output to the error.
            const output = `${error.stdout ?? ""}${error.stderr ?? ""}${error.message ?? ""}`;
            assert.ok(output.includes("TS990101"), `paris misuse failed without its diagnostic:\n${output}`);
        }
        assert.ok(parisFailed, "paris misuse (return in body) did not fail the build");
        fs.rmSync(path.join(parisProject, "src", "bad.ts"));

        // 10. Pure: with an "@conmoong/pure" entry in compilerOptions.plugins, a
        // "@pure" JSDoc tag on a top-level variable statement adds
        // /*#__PURE__*/ before calls on its initialiser's evaluation spine, and
        // the emitted JavaScript still runs correctly (annotations are comments;
        // they change nothing about program behaviour).
        const pureProject = path.join(project, "pureproj");
        fs.mkdirSync(path.join(pureProject, "src"), { recursive: true });
        fs.writeFileSync(path.join(pureProject, "package.json"), JSON.stringify({ name: "pure-smoke", private: true, type: "module" }));
        fs.writeFileSync(path.join(pureProject, "tsconfig.json"), JSON.stringify({
            compilerOptions: {
                module: "nodenext",
                target: "es2022",
                rootDir: "src",
                outDir: "dist",
                strict: true,
                plugins: [{ name: "@conmoong/pure" }],
            },
            include: ["src"],
        }, undefined, 4));
        fs.writeFileSync(path.join(pureProject, "src", "index.ts"), [
            `function createRegistry(): { size: number } {`,
            `    return { size: 0 };`,
            `}`,
            ``,
            `/**`,
            ` * @pure`,
            ` */`,
            `export const registry = createRegistry();`,
            ``,
            `console.log(\`registry.size=\${registry.size}\`);`,
            ``,
        ].join("\n"));

        runCapture(binary, ["-p", "pureproj"], { cwd: project });
        const pureJs = fs.readFileSync(path.join(pureProject, "dist", "index.js"), "utf8");
        assert.ok(pureJs.includes("/*#__PURE__*/ createRegistry()"), `pure annotation missing:\n${pureJs}`);

        const pureOutput = runCapture(process.execPath, [path.join(pureProject, "dist", "index.js")], { cwd: pureProject }).trim();
        console.log(`pure: node dist/index.js -> ${pureOutput}`);
        assert.equal(pureOutput, "registry.size=0", "pure-annotated output did not run correctly");

        // 11. WASI fallback: @conmoong/tsc-p-wasi installs and runs standalone
        // (via Node's built-in node:wasi, no separate WASI runtime), compiles a
        // fixture correctly, and propagates a real type error's exit code.
        // (The automatic root-launcher fallback-when-unsupported path itself is
        // covered by tsc-p/test/launcherWasi.test.mjs, which can
        // simulate an unsupported platform; a real install here never can,
        // since it always matches the machine running it.)
        if (!wasiPack) {
            console.log("\nskipping WASI fallback smoke check: no WASI tarball (run build.mjs first)");
        }
        else if (process.platform === "win32") {
            // Node's WASI implementation (uvwasi) has real, currently-unresolved
            // directory-enumeration bugs on Windows specifically (fd_readdir
            // reports "Function not implemented" and/or returns bad dirent data
            // on Windows — see nodejs/help#4231 and nodejs/uvwasi#148), which
            // breaks any glob-based tsconfig "include". This is a Node-level
            // limitation outside this repo's control. It's also never a real
            // user-facing path on Windows: every Windows target already ships a
            // native binary, so getExePath.js never falls through to the WASI
            // fallback there in practice. See run.mjs, which refuses to run at
            // all on win32 for the same reason.
            console.log("\nskipping WASI fallback smoke check on win32: known Node/uvwasi readdir limitation (see run.mjs)");
        }
        else {
            const wasiProject = path.join(base, "wasi proj");
            fs.mkdirSync(path.join(wasiProject, "src"), { recursive: true });
            fs.writeFileSync(path.join(wasiProject, "package.json"), JSON.stringify({ name: "wasi-smoke", private: true, type: "module" }));

            console.log(`installing WASI tarball into ${wasiProject}`);
            runCapture("npm", ["install", "--no-audit", "--no-fund", path.join(packagesOutDir, wasiPack.filename)], { cwd: wasiProject });
            const wasiBinary = localBinPath("tsc-p-wasi");

            const wasiVersion = runCapture(wasiBinary, ["--version"], { cwd: wasiProject }).trim();
            console.log(`tsc-p-wasi --version -> ${wasiVersion}`);
            assert.match(wasiVersion, new RegExp(manifest.typescriptVersion.replace(/\./g, "\\.")), "unexpected tsc-p-wasi --version output");

            fs.writeFileSync(path.join(wasiProject, "src", "index.ts"), "export function greet(name: string): string {\n    return `hi, ${name}`;\n}\n");
            fs.writeFileSync(path.join(wasiProject, "tsconfig.json"), JSON.stringify({
                compilerOptions: { module: "nodenext", target: "es2022", rootDir: "src", outDir: "dist", declaration: true, strict: true },
                include: ["src"],
            }, undefined, 4));
            runCapture(wasiBinary, ["-p", "."], { cwd: wasiProject });
            const wasiDts = fs.readFileSync(path.join(wasiProject, "dist", "index.d.ts"), "utf8");
            assert.match(wasiDts, /export declare function greet\(name: string\): string;/, `WASI compile produced unexpected declarations:\n${wasiDts}`);

            fs.writeFileSync(path.join(wasiProject, "src", "broken.ts"), `export const bad: number = "not a number";\n`);
            let wasiFailed = false;
            try {
                runCapture(wasiBinary, ["-p", "."], { cwd: wasiProject });
            }
            catch (error) {
                wasiFailed = true;
                assert.ok(error.status > 0, "expected a positive exit status for a type error under WASI");
            }
            assert.ok(wasiFailed, "expected the broken fixture to fail under WASI");
            console.log("tsc-p-wasi: compiled a fixture and propagated a type-error exit code correctly");
        }

        // 12. Standalone platform package: a user who already knows their exact
        // platform can install just @conmoong/tsc-p-<platform>-<arch> directly (no root
        // launcher at all) and run it via its own uniquely-named bin entry,
        // which points straight at the native binary — no JS in the loop.
        const platformBinaryProject = path.join(base, "platform-binary proj");
        fs.mkdirSync(platformBinaryProject, { recursive: true });
        fs.writeFileSync(path.join(platformBinaryProject, "package.json"), JSON.stringify({ name: "platform-binary-smoke", private: true, type: "module" }));
        runCapture("npm", ["install", "--no-audit", "--no-fund", path.join(packagesOutDir, platformPack.filename)], { cwd: platformBinaryProject });
        const platformBinary = localBinPath(goTarget.basename);
        const platformBinaryVersion = runCapture(platformBinary, ["--version"], { cwd: platformBinaryProject }).trim();
        console.log(`${goTarget.executable} --version -> ${platformBinaryVersion}`);
        assert.match(platformBinaryVersion, new RegExp(manifest.typescriptVersion.replace(/\./g, "\\.")), "unexpected standalone platform binary --version output");

        console.log("\nsmoke test passed");
    }
    finally {
        fs.rmSync(base, { recursive: true, force: true });
    }

}

