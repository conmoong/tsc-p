// Runs every tsc-p launcher tests (mocha/sinon/chai).
//
//   node tsc-p/scripts/launcherTest.mjs

import fs from "node:fs";
import path from "node:path";
import { run, tscpDir } from "./lib.mjs";

// JavaScript launcher tests.
if (!fs.existsSync(path.join(tscpDir, "node_modules"))) {
    run("npm", ["ci", "--no-audit", "--no-fund"], { cwd: tscpDir });
}
const mocha = path.join(tscpDir, "node_modules", ".bin", process.platform === "win32" ? "mocha.cmd" : "mocha");
run(mocha, ["test/**/*.test.mjs"], { cwd: tscpDir });
