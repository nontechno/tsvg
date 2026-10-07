package main

import (
	"encoding/xml"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
)

func decoder(s string) *xml.Decoder {
	d := xml.NewDecoder(strings.NewReader(s))
	d.CharsetReader = func(_ string, r io.Reader) (io.Reader, error) { return r, nil }
	return d
}

func wellFormed(t *testing.T, s string) {
	t.Helper()
	d := decoder(s)
	for {
		if _, err := d.Token(); err == io.EOF {
			return
		} else if err != nil {
			t.Fatalf("output is not well-formed XML: %v", err)
		}
	}
}

// noLiteralColors fails if a painting attribute still holds a bare color.
func noLiteralColors(t *testing.T, s string) {
	t.Helper()
	d := decoder(s)
	for {
		tok, err := d.Token()
		if err == io.EOF {
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		se, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		for _, a := range se.Attr {
			if !presAttrs[a.Name.Local] {
				continue
			}
			v := strings.ToLower(strings.TrimSpace(a.Value))
			if v == "none" || v == "transparent" || v == "currentcolor" ||
				strings.HasPrefix(v, "var(") || strings.HasPrefix(v, "url(") {
				continue
			}
			t.Errorf("literal color left in <%s %s=%q>", se.Name.Local, a.Name.Local, a.Value)
		}
	}
}

func TestParseColors(t *testing.T) {
	cases := map[string]rgba{
		"#fff":                   {255, 255, 255, 255},
		"#1E1E2E":                {0x1e, 0x1e, 0x2e, 255},
		"#ff000080":              {255, 0, 0, 0x80},
		"red":                    {255, 0, 0, 255},
		"RebeccaPurple":          {0x66, 0x33, 0x99, 255},
		"rgb(0, 128, 255)":       {0, 128, 255, 255},
		"rgb(255 0 0 / 50%)":     {255, 0, 0, 128},
		"hsl(120, 100%, 25%)":    {0, 128, 0, 255},
		"hsla(0, 100%, 50%, .5)": {255, 0, 0, 128},
	}
	for in, want := range cases {
		toks := scanColors(in, func(string) {})
		if len(toks) != 1 || toks[0].c != want {
			t.Errorf("scanColors(%q) = %+v, want %+v", in, toks, want)
		}
	}
	for _, in := range []string{"none", "transparent", "currentColor", "url(#a)", "url(#abc) none", "1px solid", "var(--x, red)"} {
		if toks := scanColors(in, func(string) {}); len(toks) != 0 {
			t.Errorf("scanColors(%q) = %+v, want nothing", in, toks)
		}
	}
	if toks := scanColors("url(#g) teal", func(string) {}); len(toks) != 1 {
		t.Errorf("paint fallback after url() not found: %+v", toks)
	}
}

func near(a, b rgba, tol int) bool {
	d := func(x, y uint8) int {
		if x > y {
			return int(x - y)
		}
		return int(y - x)
	}
	return d(a.r, b.r) <= tol && d(a.g, b.g) <= tol && d(a.b, b.b) <= tol
}

func TestMapper(t *testing.T) {
	o := defaultOptions()
	white, dark := rgba{255, 255, 255, 255}, rgba{0x12, 0x12, 0x12, 255}
	m := newMapper(white, dark, o)
	if got := m.apply(white, false); !near(got, dark, 2) {
		t.Errorf("page color must map to the target page: got %s", got.hex())
	}
	if got := m.apply(rgba{0, 0, 0, 255}, false); got.r < 0xd0 {
		t.Errorf("black -> %s, want light ink", got.hex())
	}
	prev := 256
	for v := 0; v <= 255; v += 15 { // lightness is strictly reversed
		got := int(m.apply(rgba{uint8(v), uint8(v), uint8(v), 255}, false).r)
		if got >= prev {
			t.Errorf("not monotonic at %d: %d >= %d", v, got, prev)
		}
		prev = got
	}
	if c := m.apply(rgba{255, 0, 0, 100}, false); c.a != 100 {
		t.Errorf("alpha lost: %v", c)
	}
	// ink is kept away from the page, ordinary fills are not
	mid := rgba{0x90, 0x90, 0x90, 255}
	if ink, fill := m.apply(mid, true), m.apply(mid, false); luminance(ink) <= luminance(fill) {
		t.Errorf("ink floor had no effect: ink %s fill %s", ink.hex(), fill.hex())
	}
	// gamma < 1 lifts a faint tint further from the page than linear does
	cream := rgba{0xff, 0xfa, 0xf0, 255}
	lin := o
	lin.classic()
	if a, b := m.apply(cream, false), newMapper(white, dark, lin).apply(cream, false); luminance(a) <= luminance(b) {
		t.Errorf("gamma did not lift the tint: %s vs %s", a.hex(), b.hex())
	}
	// reverse direction: a dark design becomes a light one
	r := newMapper(dark, white, o)
	if got := r.apply(rgba{0xee, 0xee, 0xee, 255}, true); got.r > 0x40 {
		t.Errorf("light ink on dark page -> %s, want dark ink", got.hex())
	}
	if got := r.apply(dark, false); !near(got, white, 2) {
		t.Errorf("dark page -> %s, want the light page", got.hex())
	}
}

func TestContrast(t *testing.T) {
	if r := contrast(rgba{0, 0, 0, 255}, rgba{255, 255, 255, 255}); r < 20.9 || r > 21.1 {
		t.Errorf("black on white = %.2f, want 21", r)
	}
}

const labeled = `<svg xmlns="http://www.w3.org/2000/svg" width="100" height="40">
<g transform="translate(4 36)">
<polygon fill="#d7301f" stroke="black" points="0,-30 0,-10 80,-10 80,-30"/>
<text text-anchor="start" x="5" y="-16" font-size="10">on red</text>
<text text-anchor="middle" x="40" y="-2" font-size="10">on page</text>
</g>
</svg>`

func TestContrastPairsAndFix(t *testing.T) {
	res, err := transform(labeled, defaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	if res.texts != 2 || len(res.pairs) != 2 {
		t.Fatalf("texts=%d pairs=%+v", res.texts, res.pairs)
	}
	onRed := res.pairs[0]
	for _, p := range res.pairs {
		if strings.Contains(p.bg, "fill-d7301f") {
			onRed = p
		}
	}
	if !strings.Contains(onRed.bg, "fill-d7301f") {
		t.Fatalf("text not paired with the red polygon: %+v", res.pairs)
	}

	o := defaultOptions()
	o.fixContrast = true
	fixed, err := transform(labeled, o)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range fixed.pairs {
		if p.dark < o.minRatio {
			t.Errorf("still below threshold after fix: %+v", p)
		}
		if p.light < 4 {
			t.Errorf("the input theme must not change: %+v", p)
		}
	}
	if len(fixed.adjustments) == 0 && onRed.dark < o.minRatio {
		t.Error("expected an adjustment")
	}
}

const small = `<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10">
<style>.a { fill: #FF0000; stroke: rgb(0, 128, 255) } @media print { .b { fill: teal } }</style>
<rect class="a" width="5" height="5"/>
<rect class="b" style="fill:#abc; stroke: blue" width="5" height="5"/>
<text x="1" y="1">hi</text>
<circle r="2" fill="none" stroke="transparent"/>
<linearGradient id="g"><stop offset="0"/><stop offset="1" stop-color="#123"/></linearGradient>
</svg>`

func TestSmall(t *testing.T) {
	res, err := transform(small, defaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	wellFormed(t, res.out)
	noLiteralColors(t, res.out)
	// red, azure-ish stroke, teal, #abc, blue, #123, plus implicit black
	if len(res.palette) != 7 {
		t.Errorf("got %d colors, want 7:\n%s", len(res.palette), res.out)
	}
	for _, want := range []string{
		`fill: var(--fill-ff0000, #FF0000)`,
		`stroke: var(--stroke-0080ff, rgb(0, 128, 255))`,
		`fill: var(--fill-008080, teal)`,
		`style="fill:var(--fill-aabbcc, #abc); stroke: var(--stroke-0000ff, blue)"`,
		`<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10" fill="var(--`,
		`<stop offset="0" stop-color="var(--`,
		`prefers-color-scheme: dark`,
	} {
		if !strings.Contains(res.out, want) {
			t.Errorf("output lacks %q:\n%s", want, res.out)
		}
	}
	if _, err := transform(res.out, defaultOptions()); !errors.Is(err, errAlreadyThemed) {
		t.Errorf("second run: err = %v, want errAlreadyThemed", err)
	}
}

func TestBaseDark(t *testing.T) {
	o := defaultOptions()
	o.base = "dark"
	res, err := transform(`<svg xmlns="http://www.w3.org/2000/svg"><rect fill="#000"/></svg>`, o)
	if err != nil {
		t.Fatal(err)
	}
	ci := res.palette[0]
	if ci.dark != "#000000" || ci.light == ci.dark {
		t.Errorf("base=dark: light=%s dark=%s", ci.light, ci.dark)
	}
}

func TestSampleFile(t *testing.T) {
	b, err := os.ReadFile("testdata/two.svg")
	if err != nil {
		t.Skip("testdata/two.svg missing")
	}
	res, err := transform(string(b), defaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	wellFormed(t, res.out)
	noLiteralColors(t, res.out)
	if len(res.palette) != 7 {
		t.Errorf("got %d colors, want 7", len(res.palette))
	}
}

func TestParseColorMap(t *testing.T) {
	m, warns, err := parseColorMap("# comment\n\n#969696 #505050 // trailing\nrgb(0, 128, 255) teal # note\nred  #00f\nred #0f0\n")
	if err != nil {
		t.Fatal(err)
	}
	if got := m[rgba{0x96, 0x96, 0x96, 255}].to; got != (rgba{0x50, 0x50, 0x50, 255}) {
		t.Errorf("#969696 -> %v", got)
	}
	if got := m[rgba{0, 128, 255, 255}].to; got != (rgba{0, 128, 128, 255}) {
		t.Errorf("rgb() entry -> %v", got)
	}
	if got := m[rgba{255, 0, 0, 255}].to; got != (rgba{0, 255, 0, 255}) {
		t.Errorf("later line must win, got %v", got)
	}
	if len(warns) != 1 {
		t.Errorf("want one duplicate warning, got %v", warns)
	}
	for _, bad := range []string{"#fff", "#fff #000 #111", "foo #fff #000", "#12345 #fff", "#fff rgb(1,2)", "transparent #fff", "#fff #000 junk"} {
		if _, _, err := parseColorMap(bad); err == nil {
			t.Errorf("parseColorMap(%q): expected an error", bad)
		}
	}
}

func TestColorMapApplied(t *testing.T) {
	o := defaultOptions()
	o.colorMap, _, _ = parseColorMap("#d7301f #aa5500\n#123456 #ffffff\n")
	res, err := transform(labeled, o)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, ci := range res.palette {
		if ci.c.hex() == "#d7301f" {
			found = true
			if ci.dark != "#aa5500" || !ci.pinned {
				t.Errorf("mapped color: dark=%s pinned=%v", ci.dark, ci.pinned)
			}
		}
	}
	if !found {
		t.Fatal("red not in palette")
	}
	if !strings.Contains(strings.Join(res.warnings, "\n"), "#123456") {
		t.Errorf("unused map entry should warn: %v", res.warnings)
	}
	o.fixContrast = true
	fixed, err := transform(labeled, o)
	if err != nil {
		t.Fatal(err)
	}
	for _, ci := range fixed.palette {
		if ci.c.hex() == "#d7301f" && ci.dark != "#aa5500" {
			t.Errorf("fix-contrast changed a mapped color: %s", ci.dark)
		}
	}
}
