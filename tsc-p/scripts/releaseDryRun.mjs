// Full local release rehearsal, with no publishing and no network beyond
// npm dev-dependency installation:
//
//   1. cross-compile every manifest target
//   2. run all Go and launcher tests
//   3. assemble and pack every npm package
//   4. verify package manifests and tarball contents
//   5. clean-room install and smoke test on the current platform
//
//   node tsc-p/scripts/releaseDryRun.mjs

import path from "node:path";
import { fileURLToPath } from "node:url";
import { getManifest, run } from "./lib.mjs";

const here = path.dirname(fileURLToPath(import.meta.url));
const manifest = getManifest();

console.log(`tsc-p release dry run: version ${manifest.version}, TypeScript ${manifest.typescriptVersion}, upstream ${manifest.upstream.commit.slice(0, 10)}`);

run(process.execPath, [path.join(here, "launcherTest.mjs")]);
run(process.execPath, [path.join(here, "build.mjs")]);
run(process.execPath, [path.join(here, "package.mjs")]);
run(process.execPath, [path.join(here, "verifyPackages.mjs")]);
run(process.execPath, [path.join(here, "smoke.mjs")]);

console.log("\nrelease dry run passed:");
console.log(`- built targets: ${manifest.goTargets.map(t => t.name).join(", ")}`);
console.log(`- executed natively: ${process.platform}/${process.arch} only (other targets are cross-compiled; CI runs them on native runners); the WASI fallback ran for real via node:wasi`);
