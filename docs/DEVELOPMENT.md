# Development

## Prerequisites

- Go 1.22 or newer
- [wkhtmltopdf](https://wkhtmltopdf.org/downloads.html) (ships `wkhtmltoimage` too, which Tickr uses to measure content height)
- No Node.js: the GUI is plain HTML/CSS/JS embedded with `go:embed`.

## Build, test, run

```sh
make            # build/tickr(.exe)
make test       # go vet + go test ./...
make serve      # start the GUI from the repo folder
make run        # generate one report
make release    # goreleaser snapshot for all platforms
```

During development the server must be restarted to pick up Go or web changes (`taskkill /IM tickr.exe` on Windows, `pkill tickr` elsewhere).

## Layout

| Path | What lives there |
|------|------------------|
| `cmd/tickr/main.go` | CLI: `serve`, `open`, `generate [--print]`, `modules`, `version`; data-folder resolution |
| `internal/config` | `Config` struct, JSON store with atomic writes, `.env` import, secret redaction/merge |
| `internal/modules/module.go` | `Module` interface, option `Field` schema, `Env`, registry, default layout, template helpers |
| `internal/modules/*.go` | Sections grouped by theme: `core`, `weather`, `news`, `fun`, `images`, `misc`, `ha`, `kids` |
| `internal/connectors` | External clients; each takes plain config structs so it is testable with `httptest` |
| `internal/render` | Page CSS/template, image externalisation, wkhtmltopdf/wkhtmltoimage driver, page-count check, print command |
| `internal/scheduler` | Time-of-day scheduler with once-per-day guard |
| `internal/server` | `net/http` routes and the embedded GUI under `web/` |
| `internal/kids` | Maze generator (recursive backtracker) and SVG helpers |
| `internal/wxicons` | Outline weather glyphs as inline SVG, mapped from Tomorrow.io codes |
| `internal/astro` | Meeus algorithms for equinoxes, solstices, new and full moons (tested against USNO times) |
| `internal/imaging` | Box-filter resize, Floyd–Steinberg dither, PNG/JPEG encode, data URIs |
| `assets/weathercodes` | Icon PNGs named `<tomorrow.io code><0 day|1 night>.png`; Open-Meteo WMO codes are mapped onto them |
| `legacy/` | Tickr 1.x sources, untouched, for reference |

## Conventions

- A module never panics the run: return an error and the renderer logs and skips it.
- Return `modules.EmptySection(id)` when there is genuinely nothing to print today.
- Height estimates only need to be roughly right; the renderer measures the real height with `wkhtmltoimage`.
- Anything that should show on the report as an image must be a `data:` URI in the fragment (use `imaging.PrepareForPrint`) and wrapped with `{{url .Src}}` in the template so `html/template` does not neutralise it. The renderer turns those into files for wkhtmltopdf.
- Keep secrets out of module code; they belong in `config.Connectors` and are edited through the GUI.

## Printer calibration (Windows receipt printers)

Windows print drivers for receipt printers commonly declare a *nominal* roll
width in their paper form (e.g. "Roll Paper 80 x 297 mm") that is **wider
than the area they'll actually print into**, and a *fixed* page length that
silently truncates anything taller. Two settings exist specifically to work
around this, both in `internal/render/render.go`:

- `General.PaperWidthMM` in the config — this is the **printable** content
  width, not the roll width. A roll's usable area is typically several mm
  narrower than its nominal size on each side.
- `printerLeftMarginMM` (a Go constant, currently `3.0`) — a real PDF page
  margin reserved on the left, added on top of `PaperWidthMM` so the content
  box itself doesn't have to shrink to make room for it. Set as a native
  wkhtmltopdf `--margin-left`/`--page-width` pair, not CSS padding — CSS
  padding to the same end must be duplicated per class and is easy to miss on
  a new section (this was tried and reverted; see `contentDPI`'s comment).

To find the real numbers for a given Windows driver: the printable area and
its origin are in the driver's `.GPD` file (`Get-PrinterDriver -Name
"<driver>" | select DataFile`), under `*Feature: PaperSize` → the active
`*Option` block's `*PrintableArea` and `*PrintableOrigin` `PAIR(width,
height)` values. Those are in GPD units, not mm — calibrate the units-per-mm
ratio against a form whose real mm size you already know (e.g. the
`x 297 mm` vs `x 3276 mm` variants of the same width differ by exactly
2979mm, so `(height_3276_units - height_297_units) / 2979` gives units/mm).
Divide `PrintableArea`/`PrintableOrigin` by that ratio for real mm figures.

For the EPSON TM-T20II on an 80mm roll (`EA5MDLTMT20II.GPD`, ColumnMode
`I0`, the default): printable origin ≈3.0mm from the left edge, printable
width ≈72.0mm — matching `printerLeftMarginMM=3` and the config's
`paperWidthMm=72` almost exactly. Pushing `paperWidthMm` past ~72 on this
printer reproducibly clips the right edge; there is no more room to give,
regardless of anything in Tickr's own rendering.

Two other gotchas specific to this printer/driver combination:

- Its default paper *form* has a fixed 297mm length, silently truncating
  any report taller than that (with no error — wkhtmltopdf, the print
  command, and Windows all report success). The driver also ships a
  same-width `x 3276 mm` variant with no such cap; that must be selected as
  the printer's default in Windows' own Printing Preferences dialog — this
  is a Windows setting, not something Tickr's config controls.
- Printing through Adobe Acrobat's `-Verb Print`/`/t` command-line printing
  does not respect a PDF's own page size — it prints onto whatever paper
  form is currently the Windows default, vertically **centering** shorter
  content within it. Combined with the 3276mm form above, a normal-length
  report would print after several feet of blank paper. Tickr's default
  print command now uses [SumatraPDF](https://www.sumatrapdfreader.org/)
  instead (`-print-to "<printer>" -print-settings
  "noscale,disable-auto-rotation" -silent "{pdf}"`), which prints each job
  at its PDF's own exact page size and exits immediately — no centering, no
  lingering process. `noscale` is required (SumatraPDF's default print
  scaling would otherwise fit/shrink content to whatever paper form is
  currently selected); `disable-auto-rotation` stops it from rotating a
  page that happens to be wider than it is tall (only relevant for very
  short notes, never for a full report).

If retuning any of this, print a millimeter-ruler test strip rather than
reasoning about a full report: a `<div>` per 5mm with a `left:` position and
a text label, rendered **taller than it is wide** (a short, wide strip gets
auto-rotated — see above), through the exact same
`wkhtmltopdf`/print-command path as a real report. Reading which numbers
actually land on paper is unambiguous; eyeballing a margin on a curled
receipt against a ruler is not; estimates gathered that way during this
printer's calibration were consistently 1.5-2x the driver's own numbers.

## Tests

- `internal/config` — `.env` import, round trip, redaction/merge, validation
- `internal/modules` — reminder cron matcher
- `internal/kids` — maze solvability/perfectness, determinism, SVG output
- `internal/scheduler` — once-per-day firing, weekday filter, next-run computation
- `internal/connectors` — Home Assistant client against a fake server, task JSON parsing

Live-API modules are exercised through `POST /api/preview/{id}`; a quick loop over every id is a good smoke test after changes.
