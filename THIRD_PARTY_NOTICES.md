# Third-party licenses and notices

Reviewed on 2026-09-05 for the source checkout and Linux/amd64 builds, with and
without Hunspell. The portable Linux/arm64 build uses the same 37 dependency
modules; its notice coverage and notice checksums were also verified.
Third-party copyrights and licenses remain with their owners.
The project license does not replace them.

TTYloom’s original code, documentation and artwork use the [MIT license](LICENSE.md).
Copied code, dependencies and data retain their own licenses, including the
LGPL-3.0-only and MIT Hunspell binding, Unicode License V3 emoji data, and MIT
Catppuccin palette described below. The project’s MIT grant does not relicense
those components.

## Go dependencies

- [Module inventory and original notices](licenses/go/README.md): all **53 modules
  declared in go.mod**, including **37 modules used by application packages or
  tests** on the reviewed platform. Nested license, copyright, patent, author and
  NOTICE files are retained, not just each module’s top-level license.
- [Machine-readable manifest](licenses/go/manifest.json): exact versions, module
  checksums, notice paths and SHA-256 hashes of the copied files.
- [Resolved module graph](licenses/go/module-graph.json): all 155 selected upstream
  module versions. A graph entry can be an upstream tool or test dependency with
  no code imported or redistributed by TTYloom.
- [Package license report](licenses/go-licenses.csv): classification with
  `go-licenses` v2.0.1, including project tests. The tool cannot classify
  `segmentio/asm` v1.2.1’s **MIT-0** text; those rows were checked against the
  original license and corrected manually. Multiple rows for a library can
  reflect separate notices bundled by that library; do not assume they are
  interchangeable license choices.
- [Go toolchain license](licenses/go-toolchain/LICENSE): Go 1.26.7 runtime and
  standard-library notice, with its [patent grant](licenses/go-toolchain/PATENTS).

The imported Go libraries have MIT, MIT-0, ISC, BSD or Apache-2.0 notices in
this review. The original files in `licenses/go/` control the precise terms.
Retain this notice file and the `licenses/` directory in a binary distribution.
Preserve copyright, attribution, patent and NOTICE material. Do not use upstream
names or trademarks to imply endorsement.

The scanner warns about Go assembly files because it cannot discover their
further dependencies. Reviewed assembly includes Go/runtime headers; the Go and
module notices are retained. The native Hunspell link and copied source below
were reviewed separately from the Go module scan.

## Copied Hunspell binding

`internal/spell/hunspell.go` was adapted from
[sthorne/go-hunspell](https://github.com/sthorne/go-hunspell) (MIT, copyright 2014
Sean Thorne), itself derived from
[akhenakh/hunspellgo](https://github.com/akhenakh/hunspellgo), whose upstream
[commit dfad81264f74](https://github.com/akhenakh/hunspellgo/commit/dfad81264f74)
added LGPL-3.0 in 2025. Treat the derived binding as **LGPL-3.0-only**, and retain
the MIT notice for Sean Thorne’s contributions. The former MIT-only description
of the local binding was incomplete.

The [original MIT notice](licenses/bindings/go-hunspell-LICENSE),
[upstream LGPL notice](licenses/bindings/hunspellgo-LICENSE),
[LGPL-3.0 text](licenses/bindings/LGPL-3.0.txt) and
[GPL-3.0 text](licenses/bindings/GPL-3.0.txt) are included. Changes to the binding
are identified in its header: reduced API, suggestion handling, lifetime and
locking fixes. Its complete modified source is in this repository.

For binaries that include the binding, distribute the matching TTYloom source
and build instructions so recipients can modify the binding and rebuild the
combined work. Do not prohibit reverse engineering to debug library changes.
A source archive from the **same commit as the binary**, plus `go.mod`, `go.sum`,
these notices and the README build commands, keeps that reconstruction path
available. If supplying a self-contained source bundle, include the corresponding
Go module source as well (for example with `go mod vendor`). A notice file alone
does not satisfy every obligation for a binary distribution.

`CGO_ENABLED=0 go build -tags nospell ./cmd/ttyloom` excludes this binding and
Hunspell from the executable. The binding’s notices still apply to a source
archive that contains the file.

## Native Hunspell and optional tools

The normal Linux build links dynamically to the user’s installed `libhunspell`.
Hunspell 1.7.2 offers LGPL-2.1/GPL-2.0/MPL-1.1 license choices. This project’s
normal linking instructions use the **LGPL-2.1** option. The
[license](licenses/native/hunspell-LGPL-2.1.txt),
[other upstream license texts](licenses/native/) and
[authors](licenses/native/hunspell-AUTHORS) are retained.

For a binary with Hunspell support, identify the shared-library requirement and
permit replacement with a compatible modified library. The standard build does
this through dynamic linking. If bundling or statically linking Hunspell,
provide its exact corresponding source and a way to relink as required by its
license. This repository does not bundle that library. Verify the license of
the specific native version you redistribute, including any changes.

Hunspell dictionaries, FFmpeg/ffprobe, clipboard tools and desktop notification
utilities are installed separately. Their binaries and data are not distributed
here. Dictionaries have their own licenses; an FFmpeg build’s license depends
on its enabled components. If you package any of them, retain their notices and
meet the source/relinking requirements of that exact package. The same applies
to native runtime libraries pulled into a container or installer. Calling an
external executable does not make it part of this source archive.

## Data and visual assets

| Component | Source and license | Included notice |
| --- | --- | --- |
| `internal/emoji/emoji.txt` | Unicode Emoji 17.0, Unicode License V3 | [Unicode license](licenses/unicode/LICENSE.txt), [source and checksum](licenses/unicode/SOURCE.md) |
| Screenshot palette in `internal/ui/shot_test.go` | Catppuccin Mocha, MIT | [Catppuccin notice](licenses/assets/catppuccin-LICENSE) |
| TTYloom logo and landscape fixtures | Created for this project; project-owned artwork | Project license applies |
| Screenshot conversations | Fictional test data, drawn by the actual UI renderer | No private conversations or third-party photos included |

The emoji generator is pinned to Unicode 17.0. Its output was compared byte for
byte with the committed table. Preserve the Unicode notice with the data or its
documentation. Fonts used to display SVG text are provided by the viewer’s
system; font files are not bundled.

OpenStreetMap tiles are an optional live service (`maps = false` by default).
The UI and saved map PNGs credit OpenStreetMap contributors and show the
[copyright URL](https://www.openstreetmap.org/copyright); the underlying data is
ODbL. Tiles are cached by coordinate without automatic expiry and fetched only
for visible maps or an explicit preview. Follow the
[tile service policy](https://operations.osmfoundation.org/policies/tiles/) and
[attribution guidelines](https://osmfoundation.org/wiki/Licence/Attribution_Guidelines)
when redistributing map images or data. Map imagery is not embedded in this
repository’s screenshots. Telegram and Discord
names identify interoperable services and do not imply endorsement.

## Refresh after changes

```bash
python3 scripts/licenses.py
go install github.com/google/go-licenses/v2@v2.0.1
go-licenses report ./... --include_tests --ignore github.com/govlog/ttyloom > /tmp/ttyloom-license-report.csv
```

Review scanner warnings, copied source, generated data, native dependencies and
all changes to the collected notices. The collector fails when a declared or
imported Go module has no notice file. It does not decide legal compatibility.
Review and remove directories for versions no longer used; do not silently
replace this reviewed report with an unexamined scanner result. Repeat the
inventory for another build platform or build tags before publishing that build.
