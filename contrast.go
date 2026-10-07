package main

import (
	"sort"
	"strconv"
	"strings"
)

// pair is a text color drawn over a background color (nil bg: the page itself).
type pair struct {
	fg, bg *colorInfo
	count  int
}

type pairStat struct {
	fg, bg      string // variable names; bg "page" for the page background
	light, dark float64
	count       int
	low         bool // below the threshold in the generated theme
}

type box struct{ x0, y0, x1, y1 float64 }

func (b box) contains(x, y float64) bool { return x >= b.x0 && x <= b.x1 && y >= b.y0 && y <= b.y1 }

func parseLen(s string) (float64, bool) {
	s = strings.TrimRight(strings.TrimSpace(s), "abcdefghijklmnopqrstuvwxyz%")
	f, err := strconv.ParseFloat(s, 64)
	return f, err == nil
}

func parseNums(s string) []float64 {
	var out []float64
	for _, f := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' || r == '\n' || r == '\r' }) {
		if v, ok := parseLen(f); ok {
			out = append(out, v)
		}
	}
	return out
}

func (e *elem) num(name string, def float64) float64 {
	if nums := parseNums(e.attrVal(name)); len(nums) > 0 {
		return nums[0]
	}
	return def
}

// shapeBox returns the bounding box of the basic filled shapes. Paths are not
// supported (their boxes would need full path parsing).
func shapeBox(e *elem) (box, bool) {
	switch strings.ToLower(e.name) {
	case "polygon", "polyline":
		n := parseNums(e.attrVal("points"))
		if len(n) < 4 {
			return box{}, false
		}
		b := box{n[0], n[1], n[0], n[1]}
		for i := 0; i+1 < len(n); i += 2 {
			b.x0, b.x1 = min(b.x0, n[i]), max(b.x1, n[i])
			b.y0, b.y1 = min(b.y0, n[i+1]), max(b.y1, n[i+1])
		}
		return b, true
	case "rect":
		x, y, w, h := e.num("x", 0), e.num("y", 0), e.num("width", 0), e.num("height", 0)
		return box{x, y, x + w, y + h}, w > 0 && h > 0
	case "circle":
		cx, cy, r := e.num("cx", 0), e.num("cy", 0), e.num("r", 0)
		return box{cx - r, cy - r, cx + r, cy + r}, r > 0
	case "ellipse":
		cx, cy, rx, ry := e.num("cx", 0), e.num("cy", 0), e.num("rx", 0), e.num("ry", 0)
		return box{cx - rx, cy - ry, cx + rx, cy + ry}, rx > 0 && ry > 0
	}
	return box{}, false
}

// coordSig identifies the coordinate system of an element; shapes and text are only
// compared when they share one (transforms are not composed).
func coordSig(e *elem) string {
	var parts []string
	for x := e; x != nil; x = x.parent {
		if tr := x.attrVal("transform"); tr != "" {
			parts = append(parts, tr)
		}
	}
	return strings.Join(parts, "|")
}

// fillColor is the color an element paints its fill with, if any.
func fillColor(e *elem) (rgba, bool) {
	v, ok := effFill(e)
	if !ok {
		return rgba{0, 0, 0, 255}, true // implicit default
	}
	toks := scanColors(v, func(string) {})
	if len(toks) == 0 || toks[0].c.a == 0 {
		return rgba{}, false
	}
	return toks[0].c, true
}

// collectPairs finds, for every <text>, the topmost filled shape around its anchor
// point. It returns the distinct (text color, background color) pairs and the number
// of text elements examined.
func (t *themer) collectPairs(doc *document) (map[[2]string]*pair, int) {
	type shape struct {
		b   box
		sig string
		ci  *colorInfo
	}
	var shapes []shape
	pairs := map[[2]string]*pair{}
	texts := 0
	for _, e := range doc.elems {
		if e.skip || e.inForeign || e.inClip {
			continue
		}
		ln := strings.ToLower(e.name)
		if b, ok := shapeBox(e); ok {
			if c, ok := fillColor(e); ok && t.reg[c.hex()] != nil {
				shapes = append(shapes, shape{b, coordSig(e), t.reg[c.hex()]})
			}
			continue
		}
		if ln != "text" {
			continue
		}
		fg, ok := fillColor(e)
		fci := t.reg[fg.hex()]
		if !ok || fci == nil {
			continue
		}
		fs := e.num("font-size", 16)
		px, py := e.num("x", 0), e.num("y", 0)-0.3*fs
		switch e.attrVal("text-anchor") {
		case "", "start":
			px += 0.2 * fs
		case "end":
			px -= 0.2 * fs
		}
		sig := coordSig(e)
		var bg *colorInfo
		for i := len(shapes) - 1; i >= 0; i-- {
			if shapes[i].sig == sig && shapes[i].b.contains(px, py) {
				bg = shapes[i].ci
				break
			}
		}
		key := [2]string{fci.c.hex(), ""}
		if bg != nil {
			key[1] = bg.c.hex()
		}
		if pairs[key] == nil {
			pairs[key] = &pair{fg: fci, bg: bg}
		}
		pairs[key].count++
		texts++
	}
	return pairs, texts
}

func (t *themer) genTheme() string {
	if t.opt.base == "dark" {
		return "light"
	}
	return "dark"
}

func (t *themer) rgbaIn(ci *colorInfo, theme string) rgba {
	if theme == t.opt.base {
		return ci.c
	}
	return ci.gen
}

func (t *themer) ratioIn(p *pair, theme string) float64 {
	bg := t.darkBG
	if theme == "light" {
		bg = t.lightBG
	}
	if p.bg != nil {
		bg = t.rgbaIn(p.bg, theme)
	}
	return contrast(t.rgbaIn(p.fg, theme), bg)
}

func sortedPairs(pairs map[[2]string]*pair) []*pair {
	out := make([]*pair, 0, len(pairs))
	for _, p := range pairs {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.fg.c.hex() != b.fg.c.hex() {
			return a.fg.c.hex() < b.fg.c.hex()
		}
		ak, bk := "", ""
		if a.bg != nil {
			ak = a.bg.c.hex()
		}
		if b.bg != nil {
			bk = b.bg.c.hex()
		}
		return ak < bk
	})
	return out
}

func (t *themer) setLightness(ci *colorInfo, l float64) {
	lab := toLab(ci.gen)
	ci.gen = labToRGBA(oklab{l, lab.a, lab.b}, ci.gen.a)
}

// fixContrast nudges colors of the generated theme until every text/background pair
// reaches the threshold: first by moving the fill toward the page, then by moving
// the ink away from it, then both. The input theme is never touched. It returns a
// description of every color it changed.
func (t *themer) fixContrast(pairs map[[2]string]*pair, m *mapper) []string {
	theme := t.genTheme()
	before := map[*colorInfo]rgba{}
	for _, ci := range t.order {
		before[ci] = ci.gen
	}
	const step, minVisible = 0.01, 0.03
	for pass := 0; pass < 6; pass++ {
		changed := false
		for _, p := range sortedPairs(pairs) {
			if t.ratioIn(p, theme) >= t.opt.minRatio || p.fg == p.bg {
				continue
			}
			fillFree := p.bg != nil && !p.bg.pinned
			if p.fg.pinned && !fillFree {
				continue
			}
			ok := func() bool { return t.ratioIn(p, theme) >= t.opt.minRatio }
			saveFg, saveBg := p.fg.gen, rgba{}
			if p.bg != nil {
				saveBg = p.bg.gen
			}
			restore := func() {
				p.fg.gen = saveFg
				if p.bg != nil {
					p.bg.gen = saveBg
				}
			}
			fixed := false
			if fillFree { // 1. fill toward the page
				for d := m.away(toLab(p.bg.gen).L); d >= minVisible && !fixed; d -= step {
					t.setLightness(p.bg, m.at(d))
					fixed = ok()
				}
				if !fixed {
					restore()
				}
			}
			if !fixed && !p.fg.pinned { // 2. ink away from the page
				for d := m.away(toLab(p.fg.gen).L); d <= m.room && !fixed; d += step {
					t.setLightness(p.fg, m.at(d))
					fixed = ok()
				}
				if !fixed {
					restore()
				}
			}
			if !fixed && fillFree && !p.fg.pinned { // 3. both
				t.setLightness(p.bg, m.at(minVisible))
				for d := m.away(toLab(p.fg.gen).L); d <= m.room && !fixed; d += step {
					t.setLightness(p.fg, m.at(d))
					fixed = ok()
				}
				if !fixed {
					restore()
				}
			}
			changed = changed || fixed
		}
		if !changed {
			break
		}
	}
	var notes []string
	for _, ci := range t.order {
		if ci.gen != before[ci] {
			notes = append(notes, "--"+ci.name+" ("+theme+"): "+before[ci].hex()+" -> "+ci.gen.hex())
		}
	}
	return notes
}

func (t *themer) pairStats(pairs map[[2]string]*pair) []pairStat {
	theme := t.genTheme()
	var out []pairStat
	for _, p := range sortedPairs(pairs) {
		s := pairStat{
			fg: "--" + p.fg.name, bg: "page", count: p.count,
			light: t.ratioIn(p, "light"), dark: t.ratioIn(p, "dark"),
		}
		if p.bg != nil {
			s.bg = "--" + p.bg.name
		}
		s.low = t.ratioIn(p, theme) < t.opt.minRatio
		out = append(out, s)
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i].dark, out[j].dark
		if t.genTheme() == "light" {
			a, b = out[i].light, out[j].light
		}
		return a < b
	})
	return out
}
