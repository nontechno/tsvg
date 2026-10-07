package main

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

const markerID = "svg-theme-colors"

var errAlreadyThemed = errors.New("file already contains the generated <style id=\"" + markerID + "\"> block")

type options struct {
	prefix  string // prepended to every variable name
	base    string // "light" or "dark": the theme the input was authored for
	lightBG string // page background of the light theme
	darkBG  string // page background of the dark theme
	paintBG bool   // also paint the page background on a standalone <svg>

	lmax   float64 // lightness of the most contrasting ink in the generated theme
	gamma  float64 // <1 lifts faint tints away from the page, 1 is linear
	chroma float64 // chroma factor when generating a dark theme
	inkMin float64 // minimum lightness distance of ink-like colors from the page

	minRatio    float64 // WCAG contrast threshold for text over its background
	fixContrast bool    // nudge the generated theme until the threshold is met

	colorMap    map[rgba]mapEntry // explicit colors for the generated theme
	mapWarnings []string          // problems found while reading the map
}

func defaultOptions() options {
	return options{
		base: "light", lightBG: "#ffffff", darkBG: "#121212",
		lmax: 0.95, gamma: 0.85, chroma: 0.8, inkMin: 0.5, minRatio: 4.5,
	}
}

// classic reproduces plain lightness inversion.
func (o *options) classic() { o.gamma, o.chroma, o.inkMin = 1, 1, 0 }

// colorInfo is one unique color of the document.
type colorInfo struct {
	c        rgba // as authored
	gen      rgba // the same color in the generated theme
	pinned   bool // gen comes from the color map and is never adjusted
	roles    map[string]int
	explicit int // occurrences written in the file
	implicit int // elements that rely on this color as a default
	name     string
	light    string
	dark     string
}

// inkShare is the fraction of this color's uses that are text, lines or borders.
func (ci *colorInfo) inkShare() float64 {
	total, ink := 0, 0
	for r, n := range ci.roles {
		total += n
		switch r {
		case "text", "stroke", "color", "border":
			ink += n
		}
	}
	if total == 0 {
		return 0
	}
	return float64(ink) / float64(total)
}

func parseBG(flagName, s string) (rgba, error) {
	toks := scanColors(s, func(string) {})
	if len(toks) != 1 || toks[0].s != 0 || toks[0].e != len(s) || toks[0].c.a != 255 {
		return rgba{}, fmt.Errorf("%s: cannot parse %q as an opaque color", flagName, s)
	}
	return toks[0].c, nil
}

type use struct {
	ci         *colorInfo
	raw        string // the color exactly as authored (becomes the var() fallback)
	start, end int
}

type implicitAttr struct {
	pos  int
	attr string
	ci   *colorInfo
}

type edit struct {
	start, end int
	text       string
}

type result struct {
	out         string
	palette     []*colorInfo
	warnings    []string
	pairs       []pairStat
	texts       int // text elements examined for contrast
	adjustments []string
	minRatio    float64
	genTheme    string
}

type themer struct {
	opt             options
	lightBG, darkBG rgba
	reg             map[string]*colorInfo
	order           []*colorInfo
	uses            []*use
	warnSet         map[string]bool
	warnings        []string
}

func (t *themer) warn(msg string) {
	if !t.warnSet[msg] {
		t.warnSet[msg] = true
		t.warnings = append(t.warnings, msg)
	}
}

func (t *themer) color(c rgba) *colorInfo {
	key := c.hex()
	if ci, ok := t.reg[key]; ok {
		return ci
	}
	ci := &colorInfo{c: c, roles: map[string]int{}}
	t.reg[key] = ci
	t.order = append(t.order, ci)
	return ci
}

func (t *themer) addUse(role string, c rgba, raw string, s, e int) {
	ci := t.color(c)
	ci.roles[role]++
	ci.explicit++
	t.uses = append(t.uses, &use{ci: ci, raw: raw, start: s, end: e})
}

func (t *themer) addImplicit(role string, c rgba) *colorInfo {
	ci := t.color(c)
	ci.roles[role]++
	ci.implicit++
	return ci
}

func (t *themer) collectDecl(prop, value string, vStart int, textElem bool) {
	role, ok := colorRole(prop, textElem)
	if !ok {
		return
	}
	for _, tk := range scanColors(value, t.warn) {
		if tk.c.a == 0 { // fully transparent: nothing to theme
			continue
		}
		t.addUse(role, tk.c, value[tk.s:tk.e], vStart+tk.s, vStart+tk.e)
	}
}

// presAttrs are the properties that matter for painting; they are also the
// presentation attributes whose values are replaced with var().
var presAttrs = map[string]bool{
	"fill": true, "stroke": true, "stop-color": true,
	"flood-color": true, "lighting-color": true, "color": true,
}

var paintsFill = map[string]bool{
	"path": true, "rect": true, "circle": true, "ellipse": true, "polygon": true,
	"polyline": true, "text": true, "tspan": true, "textpath": true, "tref": true,
}

func isTextElem(name string) bool {
	switch strings.ToLower(name) {
	case "text", "tspan", "textpath", "tref":
		return true
	}
	return false
}

func tagInsertPos(e *elem) int {
	if e.selfClose {
		return e.end - 2
	}
	return e.end - 1
}

func transform(src string, opt options) (*result, error) {
	doc, err := scanSVG(src)
	if err != nil {
		return nil, err
	}
	switch {
	case doc.themed:
		return nil, errAlreadyThemed
	case doc.root == nil:
		return nil, errors.New("no root element found")
	case doc.root.name != "svg":
		return nil, fmt.Errorf("root element is <%s>, expected <svg>", doc.root.name)
	case doc.root.selfClose:
		return nil, errors.New("the <svg> element is empty; nothing to theme")
	}

	t := &themer{opt: opt, reg: map[string]*colorInfo{}, warnSet: map[string]bool{}}
	for _, w := range opt.mapWarnings {
		t.warn(w)
	}
	for _, p := range doc.xmlStyleSheets {
		t.warn("external stylesheet <?xml-stylesheet " + strings.TrimSpace(p) + "?> is not analyzed and may override themed colors")
	}

	// 1. Embedded stylesheets: collect colors and the rules used to resolve implicit values.
	var rules []rule
	skippedDark := 0
	for _, b := range doc.styles {
		parseCSS(maskCDATA(src[b.start:b.end]), b.start, &rules, &skippedDark)
	}
	if skippedDark > 0 {
		t.warn(fmt.Sprintf("%d @media (prefers-color-scheme) block(s) already in the file were left untouched", skippedDark))
	}
	unevaluated := 0
	for _, r := range rules {
		relevant := false
		for _, d := range r.decls {
			t.collectDecl(d.prop, d.value, d.vStart, false)
			relevant = relevant || presAttrs[d.prop]
		}
		if !relevant {
			continue
		}
		for _, s := range r.sels {
			if !selectorSupported(s) && s != ":root" && s != "svg:root" {
				unevaluated++
				break
			}
		}
	}
	if unevaluated > 0 {
		t.warn(fmt.Sprintf("%d stylesheet rule(s) use complex selectors; they are themed but not considered when detecting implicit colors", unevaluated))
	}
	for _, e := range doc.elems {
		e.ruleVal, e.ruleKey = map[string]string{}, map[string][2]int{}
		if e.skip || e.inForeign {
			continue
		}
		for _, r := range rules {
			for _, s := range r.sels {
				spec, ok := matchSelector(s, e)
				if !ok {
					continue
				}
				key := [2]int{spec, r.order}
				for _, d := range r.decls {
					if !presAttrs[d.prop] {
						continue
					}
					if old, has := e.ruleKey[d.prop]; has && (old[0] > key[0] || old[0] == key[0] && old[1] > key[1]) {
						continue
					}
					e.ruleKey[d.prop], e.ruleVal[d.prop] = key, d.value
				}
			}
		}
	}

	// 2. Explicit colors in style="" and presentation attributes.
	for _, e := range doc.elems {
		if e.skip {
			continue
		}
		textElem := isTextElem(e.name)
		if sa := e.attrOf("style"); sa != nil {
			e.styleDecls = parseDecls(sa.val, sa.vStart)
			for _, d := range e.styleDecls {
				t.collectDecl(d.prop, d.value, d.vStart, textElem)
			}
		}
		if e.inForeign {
			continue
		}
		for _, a := range e.attrs {
			if presAttrs[a.name] {
				t.collectDecl(a.name, a.val, a.vStart, textElem)
			}
		}
		switch strings.ToLower(e.name) {
		case "animate", "set", "animatecolor":
			if presAttrs[e.attrVal("attributeName")] {
				t.warn("SMIL animation of color attributes is not themed")
			}
		}
	}

	// 3. Implicit colors: what the renderer uses when nothing was specified.
	black, white := rgba{0, 0, 0, 255}, rgba{255, 255, 255, 255}
	var implicit []implicitAttr
	rootFill := false
	for _, e := range doc.elems {
		if e.skip || e.inForeign || e.inClip {
			continue
		}
		ln := strings.ToLower(e.name)
		switch {
		case paintsFill[ln]:
			if _, ok := effFill(e); !ok {
				role := "fill"
				if isTextElem(ln) {
					role = "text"
				}
				t.addImplicit(role, black)
				rootFill = true
			}
		case ln == "stop":
			if _, ok := e.own("stop-color"); !ok {
				implicit = append(implicit, implicitAttr{tagInsertPos(e), "stop-color", t.addImplicit("stop", black)})
			}
		case ln == "feflood" || ln == "fedropshadow":
			if _, ok := e.own("flood-color"); !ok {
				implicit = append(implicit, implicitAttr{tagInsertPos(e), "flood-color", t.addImplicit("flood", black)})
			}
		case ln == "fediffuselighting" || ln == "fespecularlighting":
			if _, ok := e.own("lighting-color"); !ok {
				implicit = append(implicit, implicitAttr{tagInsertPos(e), "lighting-color", t.addImplicit("lighting", white)})
			}
		}
	}
	if rootFill {
		// One inherited attribute on <svg> covers every element that relied on the default fill.
		implicit = append(implicit, implicitAttr{tagInsertPos(doc.root), "fill", t.color(black)})
	}

	// 4. Page backgrounds anchor the mapping; they are painted only with -bg.
	if t.lightBG, err = parseBG("-light-bg", opt.lightBG); err != nil {
		return nil, err
	}
	if t.darkBG, err = parseBG("-dark-bg", opt.darkBG); err != nil {
		return nil, err
	}
	srcBG, tgtBG := t.lightBG, t.darkBG
	if opt.base == "dark" {
		srcBG, tgtBG = t.darkBG, t.lightBG
	}
	var bgCI *colorInfo
	if opt.paintBG {
		bgCI = t.addImplicit("bg", srcBG)
	}

	if len(t.order) == 0 {
		return nil, errors.New("no themeable colors found")
	}

	// 5. Names, the generated theme, contrast analysis.
	t.assignNames()
	m := newMapper(srcBG, tgtBG, opt)
	for _, ci := range t.order {
		ci.gen = m.apply(ci.c, ci.inkShare() >= 0.7)
	}
	if bgCI != nil {
		bgCI.gen = tgtBG
	}
	t.applyColorMap()
	pairs, texts := t.collectPairs(doc)
	var adjustments []string
	if opt.fixContrast {
		adjustments = t.fixContrast(pairs, m)
	}
	for _, ci := range t.order {
		if opt.base == "dark" {
			ci.light, ci.dark = ci.gen.hex(), ci.c.hex()
		} else {
			ci.light, ci.dark = ci.c.hex(), ci.gen.hex()
		}
	}
	palette := append([]*colorInfo(nil), t.order...)
	sort.Slice(palette, func(i, j int) bool { return palette[i].name < palette[j].name })

	// 6. Edits on the original text.
	var edits []edit
	for _, u := range t.uses {
		edits = append(edits, edit{u.start, u.end, fmt.Sprintf("var(--%s, %s)", u.ci.name, u.raw)})
	}
	for _, im := range implicit {
		edits = append(edits, edit{im.pos, im.pos, fmt.Sprintf(` %s="var(--%s, %s)"`, im.attr, im.ci.name, im.ci.c.hex())})
	}
	edits = append(edits, edit{doc.root.end, doc.root.end, "\n" + buildStyle(palette, bgCI)})

	sort.SliceStable(edits, func(i, j int) bool { return edits[i].start < edits[j].start })
	var b strings.Builder
	pos := 0
	for _, ed := range edits {
		if ed.start < pos {
			return nil, errors.New("internal error: overlapping edits")
		}
		b.WriteString(src[pos:ed.start])
		b.WriteString(ed.text)
		pos = ed.end
	}
	b.WriteString(src[pos:])

	return &result{
		out: b.String(), palette: palette, warnings: t.warnings,
		pairs: t.pairStats(pairs), texts: texts, adjustments: adjustments,
		minRatio: opt.minRatio, genTheme: t.genTheme(),
	}, nil
}

// assignNames builds variable names from how each color is used plus its hex value.
func (t *themer) assignNames() {
	taken := map[string]bool{}
	for _, ci := range t.order {
		type rc struct {
			role string
			n    int
		}
		var rs []rc
		for r, n := range ci.roles {
			rs = append(rs, rc{r, n})
		}
		sort.Slice(rs, func(i, j int) bool {
			if rs[i].n != rs[j].n {
				return rs[i].n > rs[j].n
			}
			return rs[i].role < rs[j].role
		})
		var parts []string
		for i := 0; i < len(rs) && i < 2; i++ {
			parts = append(parts, rs[i].role)
		}
		hex := ci.c.hexRGB()
		if ci.c.a != 255 {
			hex += fmt.Sprintf("-a%02x", ci.c.a)
		}
		name := t.opt.prefix + strings.Join(parts, "-and-") + "-" + hex
		for base, k := name, 2; taken[name]; k++ {
			name = fmt.Sprintf("%s-%d", base, k)
		}
		taken[name] = true
		ci.name = name
	}
}

func buildStyle(palette []*colorInfo, bg *colorInfo) string {
	pad := 0
	for _, ci := range palette {
		pad = max(pad, len(ci.name)+3)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "<style id=%q>\n", markerID)
	b.WriteString("  /* Generated by svgtheme: one variable per unique color, set per color scheme. */\n")
	for _, scheme := range []string{"light", "dark"} {
		fmt.Fprintf(&b, "  @media (prefers-color-scheme: %s) {\n    :root {\n", scheme)
		for _, ci := range palette {
			v := ci.light
			if scheme == "dark" {
				v = ci.dark
			}
			fmt.Fprintf(&b, "      %-*s %s;\n", pad, "--"+ci.name+":", v)
		}
		b.WriteString("    }\n  }\n")
	}
	if bg != nil {
		fmt.Fprintf(&b, "  svg:root { background-color: var(--%s, %s); }\n", bg.name, bg.c.hex())
	}
	b.WriteString("</style>")
	return b.String()
}

// applyColorMap overrides the generated colors the user listed in the color map.
// Entries whose color never occurs in the SVG are reported.
func (t *themer) applyColorMap() {
	used := map[rgba]bool{}
	for _, ci := range t.order {
		if e, ok := t.opt.colorMap[ci.c]; ok {
			ci.gen, ci.pinned = e.to, true
			used[ci.c] = true
		}
	}
	var unused []mapEntry
	from := map[int]rgba{}
	for c, e := range t.opt.colorMap {
		if !used[c] {
			unused = append(unused, e)
			from[e.line] = c
		}
	}
	sort.Slice(unused, func(i, j int) bool { return unused[i].line < unused[j].line })
	for _, e := range unused {
		t.warn(fmt.Sprintf("color map line %d: %s does not occur in the SVG", e.line, from[e.line].hex()))
	}
}
