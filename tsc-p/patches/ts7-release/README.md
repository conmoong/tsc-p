# Lane overrides: `ts7-release`

Patches applied by `tsc-p/scripts/portPatch.mjs` **after** the main patch, when
porting onto upstream's `ts7-release` line.

They exist for the narrow case where a tsc-p change cannot be expressed as one
diff that applies to both upstream lanes — usually because the surrounding
upstream code differs between `main` and the release branch. The file is then
excluded from the main patch for this lane (see `LANE_EXCLUDES` in
`portPatch.mjs`) and supplied here instead, written against the release line's
own context.

These live in `dev` deliberately: the `release-candidate` branch is derived and
force-rebuilt on every port, so anything fixed there directly is destroyed on
the next run. Fixes must land here to survive.

| Patch | Why |
|---|---|
| `0001-inherit-plugins-across-extends.patch` | The `compilerOptions.plugins`/`extends` fix. On `main` it sits next to the `contentMappers` handling, which does not exist on `ts7-release`, so the `main` diff cannot apply. Delete this once the fix is accepted upstream, or once the lane picks up `contentMappers`. |
| `0002-emit-test-harness-api.patch` | The `internal/tscp` emit-test harness calls `compiler.NewCompilerHost` and `ParsedOptions`, whose signatures/locations differ between `main` and `ts7-release`. Only the tests are affected — the plugins themselves compile unchanged on both lanes. Delete once the lane picks up the newer API. |
