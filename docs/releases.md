# Publishing a release

TTYloom publishes portable Linux binaries for x86-64 (`amd64`) and ARM64
(`arm64`). They use `CGO_ENABLED=0` and `nospell`; Hunspell is available in source
builds. FFmpeg, clipboard tools and dictionaries are installed separately.

Each version contains two binary archives, a source archive from the same
commit, and `SHA256SUMS`. Binary archives include the READMEs, documentation,
dependency notices and license texts. `BUILD.txt` records the version, commit,
Go toolchain and build flags; `ttyloom --version` prints the version and commit.

## Prepare and publish

1. Update the changelog and the version in both READMEs. Review dependency
   notices if `go.mod`, copied code, assets or build targets have changed.
2. Run the checks in `.github/workflows/ci.yml`, then commit the release changes.
3. Tag that commit and push it, replacing `v1.1.0` below with the new version:

   ```bash
   git tag -a v1.1.0 -m 'TTYloom v1.1.0'
   git push origin main
   git push origin v1.1.0
   ```

4. Wait for CI and the Release workflow. The release job tests the portable
   build, creates the archives, verifies checksums, runs the x86-64 binary and
   runs the ARM64 binary with QEMU, then uploads a **draft** GitHub release.
5. Review its files and release notes, then publish the draft from GitHub or:

   ```bash
   gh release edit v1.1.0 --draft=false --latest
   ```

The CLI checks do not replace testing live accounts or testing on native ARM64
hardware. Do not overwrite published assets or move a published tag: release
a new version for corrections.

## Build locally

On Linux, with the Go version from `go.mod`, Git, Bash, GNU tar and gzip:

```bash
bash scripts/release.sh v1.1.0
cd dist/v1.1.0
sha256sum --check SHA256SUMS
```

The tag must point to the current commit, the working tree must be clean, and
the output directory must be empty. The script builds an archive of committed
source, so local files cannot enter the packages. It refuses tracked internal
working documents. Output is stored under `dist/`, which Git ignores.

Builds use `-trimpath` and record the commit explicitly. Archive timestamps,
ordering and ownership are fixed. Use the same Go patch version when comparing
checksums between machines. `SHA256SUMS` detects corrupted or changed downloads;
it is not a digital signature.
