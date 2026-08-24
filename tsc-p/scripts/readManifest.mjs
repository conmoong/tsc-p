// Read the manifest and output it as JSON for Github Actions to consume. 
// This is used in the CI workflow to pass the manifest to matrix jobs.
// Usage: node tsc-p/scripts/readManifest.mjs

import { getManifest } from "./lib.mjs";

// Only name/ghActionRunner are needed to drive the matrix; the rest of each
// platform object holds back-references to goTargets (which themselves
// reference the platform), which is circular and can't be JSON-serialized.
const platforms = getManifest().platforms.map(({ name, ghActionRunner }) => ({ name, ghActionRunner }));

process.stdout.write(JSON.stringify(platforms) + "\n");