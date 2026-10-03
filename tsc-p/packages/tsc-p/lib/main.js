import childProcess from "node:child_process";
import getExePath, { UnsupportedPlatformError } from "./getExePath.js";
import getWasiFallback from "./getWasiFallback.js";

const args = process.argv.slice(2);

let exe;
try {
    exe = await getExePath();
}
catch (error) {
    if (error instanceof UnsupportedPlatformError) {
        const wasiRun = await getWasiFallback();
        if (wasiRun) {
            // See the @conmoong/tsc-p-wasi package's own lib/run.mjs.
            process.exit(await wasiRun(args));
        }
    }
    console.error(error instanceof Error ? error.message : String(error));
    process.exit(1);
}

if (process.platform !== "win32" && typeof process.execve === "function") {
    // Replaces this process entirely, so the child's exit status and signal
    // behaviour are preserved exactly. Available since Node 22.15.
    try {
        process.execve(exe, [exe, ...args]);
    }
    catch {
        // Not available or not permitted; fall through to execFileSync.
    }
}

try {
    childProcess.execFileSync(exe, args, { stdio: "inherit" });
}
catch (error) {
    if (error.status !== null && error.status !== undefined) {
        process.exitCode = error.status;
    }
    else if (error.signal) {
        // Re-raise the child's fatal signal so callers observe the same
        // termination reason we did.
        process.kill(process.pid, error.signal);
    }
    else {
        throw error;
    }
}
