import fs from "node:fs/promises";
import pathUtils from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

const WASI_PACKAGE_NAME = '@conmoong/tsc-p-wasi';

/**
 * Resolves the WASI fallback package, if installed. Unlike getExePath,
 * "not found" is not an error here — it is the expected outcome on a
 * supported platform (where the fallback is never installed) and is
 * signalled by returning null so main.js can fall through to its own
 * unsupported-platform error message.
 *
 * @param {object} [overrides] test seams; production callers pass nothing
 * @param {(specifier: string) => string} [overrides.resolve] returns a file URL string
 * @returns {Promise<((args: string[]) => Promise<number>) | null>}
 */
export default async function getWasiFallback(overrides = {}) {
    const resolve = overrides.resolve ?? (specifier => import.meta.resolve(specifier));    

    let packageJsonPath;
    let runnerUrl;
    try {
        packageJsonPath = fileURLToPath(resolve(`${WASI_PACKAGE_NAME}/package.json`));
        runnerUrl = resolve(WASI_PACKAGE_NAME);
    }
    catch {
        return null;
    }

    const wasmPath = pathUtils.join(pathUtils.dirname(packageJsonPath), "tsc-p-wasi.wasm");
    try {
        await fs.access(wasmPath);
    }
    catch {
        return null;
    }

    const { run } = await import(runnerUrl.startsWith("file://") ? runnerUrl : pathToFileURL(runnerUrl).href);
    return run.bind(null, wasmPath);
}
