// Executes the tsc-p WASI (wasip1/wasm) binary in-process using Node's
// built-in node:wasi module — no external WASI runtime (wasmtime, wasmer,
// ...) needs to be installed. This is the universal fallback for any
// platform not covered by tsc-p's native optionalDependencies packages.
//
// preopens: { "/": "/" } maps the WASI guest's root directory onto the
// real host root, giving the compiled program the same effectively
// unrestricted filesystem access a native binary has (absolute paths
// resolve exactly as they would to real syscalls) — this is a drop-in
// replacement for the native binary, not a sandboxed execution mode.
//
// WASI preview1 has no real getcwd() syscall: Go's wasip1 runtime resolves
// relative paths (e.g. a bare "tsconfig.json") against the PWD environment
// variable it's given, not any host syscall. process.env.PWD is only
// correct when a real shell has just `cd`-ed — a parent process spawning
// this with an explicit `cwd` option (e.g. child_process's cwd, which sets
// the OS-level working directory directly) leaves an inherited, now-stale
// PWD in process.env untouched. Overriding PWD with process.cwd() here
// makes relative-path resolution correct regardless of how this process
// arrived at its current directory.
//
// Not supported on Windows: Node's WASI implementation (uvwasi) has real,
// currently-unresolved directory-enumeration bugs on Windows specifically
// (fd_readdir reports "Function not implemented" and/or returns bad dirent
// data — see nodejs/help#4231 and nodejs/uvwasi#148), which breaks any
// glob-based tsconfig "include". That's a defect in Node's C++ WASI
// binding, not something fixable from here. It's also never a real
// user-facing path: every Windows target tsc-p ships already has a native
// binary, so getExePath.js never falls through to this fallback on
// Windows in practice — refusing outright is clearer than silently
// mismapping paths for a platform this can't actually support.
import { readFile } from "node:fs/promises";
import { WASI } from "node:wasi";

/**
 * @param {string} wasmPath absolute path to the tsc-p .wasm binary
 * @param {string[]} args CLI arguments, NOT including the program name
 * @returns {Promise<number>} the program's exit code
 */
export async function run(wasmPath, args) {
    if (process.platform === "win32") {
        throw new Error(
            "tsc-p-wasi does not support Windows: Node's WASI implementation has unresolved "
            + "directory-listing bugs on Windows (see nodejs/help#4231 and nodejs/uvwasi#148). "
            + "Every Windows target already has a native @conmoong/tsc-p-<platform> package; "
            + "install that instead."
        );
    }

    const wasi = new WASI({
        version: "preview1",
        args: ["tsc-p", ...args],
        env: { ...process.env, PWD: process.cwd() },
        preopens: { "/": "/" },
    });

    const wasmBuffer = await readFile(wasmPath);
    const { instance } = await WebAssembly.instantiate(wasmBuffer, wasi.getImportObject());
    return wasi.start(instance);
}
