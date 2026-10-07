# tsvg

Make an SVG follow the viewer's light/dark color scheme.

`tsvg` reads an SVG, finds every unique color it uses (fills, strokes, text,
gradient stops, filter colors, and the *implicit* default colors), defines one CSS
custom property per color in a `<style>` block, and rewrites the drawing to use
those properties. It also generates a second palette for the opposite theme and
emits both under `@media (prefers-color-scheme: ...)`.

Go standard library only. No dependencies.

## Build

```
go build -o tsvg .
go test ./...
```

## Quick start

```
tsvg diagram.svg                 # writes diagram.themed.svg, prints a report
tsvg -o out.svg diagram.svg      # choose the output file ('-' for stdout)
tsvg -fix-contrast diagram.svg   # also nudge the dark palette until text is readable
tsvg -map color.map diagram.svg  # pin specific colors (see below)
```

Flags may come before or after the input file.

## What it does

Input:

```xml
<svg xmlns="http://www.w3.org/2000/svg" width="100" height="40">
  <rect width="80" height="30" fill="#d9d9d9" stroke="#737373"/>
  <text x="5" y="20">hello</text>
</svg>
```

Output (abridged):

```xml
<svg xmlns="http://www.w3.org/2000/svg" width="100" height="40" fill="var(--text-000000, #000000)">
  <style id="svg-theme-colors">
    @media (prefers-color-scheme: light) { :root { --fill-d9d9d9: #d9d9d9; --stroke-737373: #737373; --text-000000: #000000; } }
    @media (prefers-color-scheme: dark)  { :root { --fill-d9d9d9: #2f2f2f; --stroke-737373: #999999; --text-000000: #eeeeee; } }
  </style>
  <rect width="80" height="30" fill="var(--fill-d9d9d9, #d9d9d9)" stroke="var(--stroke-737373, #737373)"/>
  <text x="5" y="20">hello</text>
</svg>
```

- Every explicit color becomes `var(--name, original)`. The original stays as the
  fallback, so renderers without CSS-variable support look exactly as before.
- Variable names are `--<role>-<hex>` (for example `--fill-d9d9d9`). A color used
  in several roles gets a combined name such as `--fill-and-stroke-737373`.
  Semi-transparent colors get an `-aXX` suffix.
- **Implicit colors**: elements with no `fill` default to black. This is themed
  with one inherited `fill` attribute on the root `<svg>`. Gradient stops and
  filter flood/lighting colors are handled the same way.
- Left alone: `none`, `transparent`, `currentColor`, `url(...)` paints, and any
  `@media (prefers-color-scheme)` blocks already in the file.
- Colors in `<style>` blocks and `style="..."` attributes are rewritten too (simple
  selectors only: type, `.class`, `#id`).
- The rest of the file is preserved byte for byte.
- Running it on its own output is refused; run it on the original.

## How the other theme is chosen

By default the input is treated as a **light** design on a white page, and a dark
palette is generated for a `#121212` page.

A plain "invert the lightness" tends to give glaring, over-saturated results. Instead,
each color is placed by its perceptual distance (OKLab) from the page background:

- A color's distance from the old background becomes the same distance from the new
  one, so faint tints stay faint and strong colors stay strong.
- Distance is passed through a curve (`-gamma`) so very faint tints are lifted just
  enough to remain visible.
- Chroma is reduced a little for dark themes (`-chroma`) so reds and blues are calmer.
- Colors that are mostly "ink" (text, lines) are kept at least `-ink` away from the
  page so they never fade into it.

`-classic` switches all of that off and does a plain lightness inversion.

### Contrast check

For every `<text>` element, tsvg finds the filled shape behind it (rect, circle,
ellipse, polygon, polyline) and computes the WCAG contrast ratio in both themes. The
report lists pairs that fall below `-min-ratio` (default 4.5). With `-fix-contrast`
it nudges the generated palette, moving the fill toward the page first, then the
ink away from it, until the pairs pass. The theme you supplied is never altered.

Paths are not analyzed for what sits behind text, and transforms are not composed, so
the check is a good guide rather than a guarantee.

## Color map

`-map FILE` lets you decide the generated color for specific colors instead of using
the computed one.

```
# comments start with '# ' or '//'
#969696 #505050
rgb(0, 128, 255) teal   // inline comment
```

- Each line is `FROM TO`: `FROM` is a color as it appears in the SVG, `TO` is the
  color to use for it in the generated theme.
- Any CSS color syntax works on either side: `#rgb`, `#rrggbb`, `#rrggbbaa`, named
  colors, `rgb()`, `hsl()`.
- Matching is exact, alpha included.
- Mapped colors are used as written and are never changed by `-fix-contrast` (the
  other color in a text/background pair may still be).
- They are marked with `*` in the report.
- An entry that matches no color in the SVG produces a warning. A color listed twice
  uses the later line and warns.
- Malformed lines (one color, three colors, stray text, a fully transparent FROM)
  stop the run with an error naming the line.
- With `-base dark`, `TO` goes in the generated *light* theme.

## Flags

| Flag | Default | Meaning |
|---|---|---|
| `-o FILE` | `<input>.themed.svg` | Output file; `-` for stdout |
| `-prefix S` | none | Prefix for variable names, e.g. `svg-` gives `--svg-fill-d9d9d9` |
| `-base light\|dark` | `light` | Theme the input was drawn for; the other is generated |
| `-light-bg C` | `#ffffff` | Page background of the light theme |
| `-dark-bg C` | `#121212` | Page background of the dark theme |
| `-bg` | off | Also paint the page background (standalone SVG only) |
| `-lmax X` | `0.95` | OKLab lightness of the most contrasting ink in the generated theme |
| `-gamma X` | `0.85` | Distance curve; below 1 lifts faint tints, 1 is linear |
| `-chroma X` | `0.8` | Saturation factor for a generated dark theme (1 keeps it) |
| `-ink X` | `0.5` | Minimum lightness distance of text/line colors from the page |
| `-min-ratio X` | `4.5` | WCAG contrast threshold for text over its background |
| `-fix-contrast` | off | Nudge the generated theme until text meets `-min-ratio` |
| `-map FILE` | none | Color map file (see above) |
| `-classic` | off | Plain lightness inversion (`-gamma 1 -chroma 1 -ink 0`) |
| `-q` | off | Suppress the report |

## The report

The report goes to stderr:

- a table of every variable with its light and dark value, how many explicit and
  implicit uses it has, and the roles it plays;
- the contrast summary and the worst text/background pairs;
- any contrast adjustments made;
- warnings (unused map entries, external stylesheets that may override colors,
  unsupported selectors, and so on).

## Limitations

- Not a full CSS engine: only simple selectors are evaluated; stylesheets linked from
  outside the file are not analyzed.
- `<foreignObject>` content and clip paths are skipped.
- Contrast pairing ignores `<path>` shapes and composed transforms.
- Rendering of the result depends on the viewer honoring `prefers-color-scheme` for
  SVG. When an SVG is used through `<img>`, browsers apply the OS/browser scheme; when
  it is inlined in HTML, the page's own scheme applies.
