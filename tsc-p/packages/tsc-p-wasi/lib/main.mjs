import pathUtils from "node:path";
import { fileURLToPath } from "node:url";
import { run } from "./run.mjs";

const wasmPath = pathUtils.join(pathUtils.dirname(fileURLToPath(import.meta.url)), "..", "tsc-p-wasi.wasm");
const args = process.argv.slice(2);

try {
    process.exit(await run(wasmPath, args));
}
catch (error) {
    console.error(error instanceof Error ? error.message : String(error));
    process.exit(1);
}
