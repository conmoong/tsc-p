import { expect } from "chai";
import { parseTarListing } from "../scripts/lib.mjs";

// Regression test for a Windows CI failure: tar -tzf on Windows runners
// emits CRLF-terminated lines. Splitting only on "\n" leaves a trailing
// "\r" on every entry name, which silently breaks exact-match and
// endsWith/regex checks in verify-packages.mjs — reporting files as both
// "missing" and "unexpected" under their own correct name.
describe("parseTarListing", () => {
    it("strips CRLF line endings", () => {
        const output = "package/package.json\r\npackage/lib/tsc-p.exe\r\npackage/lib/lib.d.ts\r\n";
        expect(parseTarListing(output)).to.deep.equal(["package.json", "lib/tsc-p.exe", "lib/lib.d.ts"]);
    });

    it("handles plain LF line endings the same way", () => {
        const output = "package/package.json\npackage/lib/tsc-p\n";
        expect(parseTarListing(output)).to.deep.equal(["package.json", "lib/tsc-p"]);
    });

    it("drops the package/ tarball root prefix", () => {
        expect(parseTarListing("package/README.md\n")).to.deep.equal(["README.md"]);
    });

    it("ignores blank trailing lines", () => {
        expect(parseTarListing("package/a.txt\n\n\n")).to.deep.equal(["a.txt"]);
    });

    it("a CRLF-affected entry still satisfies endsWith and exact-match checks once parsed", () => {
        const [entry] = parseTarListing("package/lib/lib.d.ts\r\n");
        expect(entry).to.equal("lib/lib.d.ts");
        expect(entry.endsWith(".d.ts")).to.equal(true);
        expect(["lib/lib.d.ts"].includes(entry)).to.equal(true);
    });
});
