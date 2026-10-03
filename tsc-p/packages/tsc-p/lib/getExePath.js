import fs from "node:fs/promises";
import pathUtils from "node:path";
import { fileURLToPath } from "node:url";

/**
 * Thrown by getExePath when the current platform/arch pair has no native
 * target at all — distinct from every other getExePath failure (a missing
 * or corrupt install), which are real installation problems that should
 * never silently trigger the WASI fallback. main.js catches this specific
 * class to decide whether to try the WASI fallback.
 */
export class UnsupportedPlatformError extends Error {}

/**
 * Resolves the absolute path of the native tsc-p executable for the current
 * platform. Resolution order:
 *
 * 1. The TSC_P_EXE environment variable, if set (a development and testing
 *    escape hatch; it must point at an existing file).
 * 2. The matching platform package, located through Node module resolution
 *    (including Yarn Plug'n'Play) via its exported package.json.
 *
 * No network access is performed.
 *
 * @param {object} [overrides] test seams; production callers pass nothing
 * @param {string} [overrides.platform]
 * @param {string} [overrides.arch]
 * @param {(specifier: string) => string} [overrides.resolve] returns a file URL string
 * @param {Record<string, string | undefined>} [overrides.env]
 * @returns {Promise<string>}
 */
export default async function getExePath(overrides = {}) {
    const platform = overrides.platform ?? process.platform;
    const arch = overrides.arch ?? process.arch;
    const env = overrides.env ?? process.env;
    const resolve = overrides.resolve ?? (specifier => import.meta.resolve(specifier));

    if (env.TSC_P_EXE) {
        try {
            await fs.access(env.TSC_P_EXE);
            return env.TSC_P_EXE;
        }
        catch {
            throw new Error(`TSC_P_EXE is set but does not exist: ${env.TSC_P_EXE}`);
        }                
    }

    const pkg = overrides.package ?? JSON.parse(await fs.readFile(pathUtils.join(pathUtils.dirname(fileURLToPath(import.meta.url)), "..", "package.json"), "utf8"));
    const optionalDependencyNames = Object.keys(pkg.optionalDependencies ?? {});

    const packageName = `@conmoong/tsc-p-${platform}-${arch}`;
    if (!optionalDependencyNames.includes(packageName)) {
        throw new UnsupportedPlatformError(
            `tsc-p does not support ${platform}-${arch}. Supported platforms: ${optionalDependencyNames.map(name => name.substring('@conmoong/tsc-p-'.length)).join(", ")}. `
        + `Install @conmoong/tsc-p-wasi for a WASI-based fallback that runs anywhere Node's node:wasi does, or build from source.`);
    }

    let packageJsonPath;
    try {
        packageJsonPath = fileURLToPath(resolve(`${packageName}/package.json`));
    }
    catch {
        throw new Error(
            `tsc-p could not resolve its platform package ${packageName}. `
        + `This usually means optional dependencies were not installed. `
        + `tsc-p relies on optionalDependencies to deliver the native binary for your platform, `
        + `so installing with --omit=optional (npm), --no-optional, or an equivalent package-manager `
        + `setting is unsupported. Reinstall with optional dependencies enabled.`
        );
    }

    // The executable ships at the platform package's root, see that package.json's own "bin" entry (tsc-p-<platform>-<arch>),
    // which points at the same file for direct standalone use.
    let exe = pathUtils.join(pathUtils.dirname(packageJsonPath), platform === 'win32' ? `tsc-p-${platform}-${arch}.exe` : `tsc-p-${platform}-${arch}`);
    if (platform === "win32" && exe.length >= 248) {
        exe = "\\\\?\\" + exe;
    }

    try {
        await fs.access(exe);
        return exe;
    }
    catch {
        throw new Error(`tsc-p resolved ${packageName} but the executable is missing: ${exe}. The package installation appears to be corrupt; try reinstalling.`);
    }      
}
