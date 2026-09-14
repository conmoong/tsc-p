// Shared helpers for the tsc-p build and packaging scripts. Plain Node,
// no dependencies.

import { execFileSync } from "node:child_process";
import fs from "node:fs";
import pathUtils from "node:path";
import { fileURLToPath } from "node:url";

export const repoRoot = pathUtils.resolve(pathUtils.dirname(fileURLToPath(import.meta.url)), "..", "..");
export const tscpDir = pathUtils.join(repoRoot, "tsc-p");
// The Go compiler module (github.com/microsoft/TypeScript/tsc), since the
// typescript-go -> microsoft/TypeScript consolidation moved it into a `tsc/`
// subdirectory alongside the JS/TS side of the monorepo rather than at the
// repo root.
export const goDir = pathUtils.join(repoRoot, "tsc");
export const outDir = pathUtils.join(repoRoot, "built", "tscp");
export const binOutDir = pathUtils.join(outDir, "bin");
export const npmOutDir = pathUtils.join(outDir, "npm");
export const packagesOutDir = pathUtils.join(outDir, "packages");
export const libsSourceDir = pathUtils.join(goDir, "internal", "bundled", "libs");

let manifestCache;

export function getManifest() {
    if (!manifestCache) {
        manifestCache = JSON.parse(fs.readFileSync(pathUtils.join(tscpDir, "manifest.json"), "utf8"));
        // Validate platforms
        const platforms = new Map();
        let currentPlatform;
        for (const platform of manifestCache.platforms) {
            if (platforms.has(platform.name)) {
                fail(`Duplicate platform name ${platform.name} in manifest.json`);
            }            
            // Find current platform
            if (platform.nodejsPlatform === process.platform && platform.nodejsArch === process.arch) {
                platform.current = true;
                currentPlatform = platform;
            }
            platforms.set(platform.name, platform);
        };  
        // Validate go target
        const goTargets = new Map();
        const nativeGoTargets = new Map();
        const wasmGoTargets = new Map();
        for (const goTarget of manifestCache.goTargets) {
            if (goTargets.has(goTarget.name)) {
                fail(`Duplicated go target name ${goTarget.name} in manifest.json`);
            }                                    
            if (goTarget.type === "native") {
                const platform = platforms.get(goTarget.platform);
                if (!platform) {
                    fail(`Native Go target ${goTarget.name} references unknown platform ${goTarget.platform}`);
                }
                goTarget.platform = platform;
                goTarget.basename = `tsc-p-${platform.name.replace('/', '-')}`;
                goTarget.executable = goTarget.goOs === "windows" ? `${goTarget.basename}.exe` : goTarget.basename;
                goTarget.executablePath = pathUtils.join(binOutDir, goTarget.executable);
                nativeGoTargets.set(goTarget.name, goTarget);                
            } else if (goTarget.type === 'wasm') {
                if (goTarget.platform != null) {
                    fail(`Wasm Go target ${goTarget.name} should not have ${goTarget.platform}`);
                }
                goTarget.platform = undefined;
                goTarget.basename = `tsc-p-${goTarget.name.replace('/', '-')}`;
                goTarget.executable =`${goTarget.basename}.wasm`;
                goTarget.executablePath = pathUtils.join(binOutDir, goTarget.executable);
                wasmGoTargets.set(goTarget.name, goTarget);                
            } else {
                fail(`Invalid go target type for ${goTarget.name} in manifest.json`);
            }
            goTargets.set(goTarget.name, goTarget);
        };
        if (goTargets.size === 0) {
            fail('Cannot find any go target');
        }
        manifestCache.goTargets = [...goTargets.values()];
        manifestCache.wasmGoTargets = [...wasmGoTargets.values()];
        // Populate platforms nativeGoTargets and wasmGoTargets
        platforms.forEach((platform) => {
            const platformNativeGoTargets = new Set();
            nativeGoTargets.forEach((goTarget) => {
                if (goTarget.platform === platform) {
                    platformNativeGoTargets.add(goTarget);
                }
            });            
            platform.nativeGoTargets = [...platformNativeGoTargets];
            if (platform.nativeGoTargets.length === 1) {
                platform.nativeExecutable = platform.nativeGoTargets[0].executable;
            }
            platform.wasmGoTargets = [...wasmGoTargets.values()];                    
        });          
        manifestCache.platforms = [...platforms.values()];
        manifestCache.currentPlatform = currentPlatform;
    }
    return manifestCache;
}

// Path to an installed package's bin shim, relative to the project root
// (e.g. "node_modules/.bin/tsc-p" or "...tsc-p.cmd" on Windows).
//
// Deliberately relative, not joined onto an absolute project directory:
// invoking it requires shell:true on Windows (see needsWindowsShell), and
// Node's shell:true does not quote the file/args it is given — it just
// joins them with spaces before handing the string to cmd.exe. An absolute
// path through a directory containing a space (a real scenario: this
// project is exercised against exactly such a path, and a real user's
// Windows profile directory can easily contain one too) would then be
// split mid-path by cmd.exe's tokenizer. A path relative to `cwd` sidesteps
// this entirely, since `cwd` is passed to CreateProcess as its own
// parameter and never enters the command-line string cmd.exe parses.
export function localBinPath(name, platform = process.platform) {
    return pathUtils.join("node_modules", ".bin", platform === "win32" ? `${name}.cmd` : name);
}

// npm and npx are .cmd shim scripts on Windows, not native executables —
// and so is any package's bin entry once npm installs it on Windows (e.g.
// node_modules/.bin/tsc-p.cmd, a wrapper script around the real binary).
// Node refuses to spawn a .bat/.cmd file directly unless the shell option
// is set — this is deliberate (see the fix for CVE-2024-27980) and applies
// regardless of whether the .cmd extension is given explicitly or a bare
// name is resolved via PATH: without shell:true it fails, with an ENOENT
// for a bare name or an EINVAL for an explicit .cmd/.bat path. Routing the
// call through a shell lets cmd.exe do its own resolution. "go" and any
// full path to one of our own *built* binaries (e.g. built/tscp/bin/<target_name>.exe) 
// are real executables and are unaffected.
const windowsCmdShims = new Set(["npm", "npx"]);

export function needsWindowsShell(command, platform = process.platform) {
    if (platform !== "win32") {
        return false;
    }
    return windowsCmdShims.has(command) || /\.(cmd|bat)$/i.test(command);
}

export function run(command, args, options = {}) {
    console.log(`$ ${command} ${args.join(" ")}`);
    const shellOptions = needsWindowsShell(command) ? { shell: true } : {};
    return execFileSync(command, args, { stdio: "inherit", cwd: repoRoot, ...shellOptions, ...options });
}

export function runCapture(command, args, options = {}) {
    const shellOptions = needsWindowsShell(command) ? { shell: true } : {};
    return execFileSync(command, args, { encoding: "utf8", cwd: repoRoot, ...shellOptions, ...options });
}

// Parses the entry names out of `tar -tzf` output. tar on Windows runners
// emits CRLF-terminated lines; splitting on "\n" alone would leave a stray
// trailing "\r" on every entry name, breaking exact-match and
// endsWith/regex checks against them.
export function parseTarListing(output) {
    return output.split(/\r?\n/).map(line => line.trim()).filter(Boolean).map(line => line.replace(/^package\//, ""));
}

export function fail(message) {
    console.error(`error: ${message}`);
    process.exit(1);
}