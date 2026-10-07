package main

import "math"

func toLab(c rgba) oklab {
	return toOKLab(
		srgbToLinear(float64(c.r)/255),
		srgbToLinear(float64(c.g)/255),
		srgbToLinear(float64(c.b)/255),
	)
}

// labToRGBA converts OKLab to sRGB. A color outside the sRGB gamut keeps its
// lightness and hue and loses just enough chroma to fit.
func labToRGBA(l oklab, alpha uint8) rgba {
	l.L = clamp01(l.L)
	try := func(k float64) (r, g, b float64, ok bool) {
		r, g, b = fromOKLab(oklab{l.L, l.a * k, l.b * k})
		const eps = 1e-4
		ok = r >= -eps && r <= 1+eps && g >= -eps && g <= 1+eps && b >= -eps && b <= 1+eps
		return
	}
	r, g, b, ok := try(1)
	if !ok {
		lo, hi := 0.0, 1.0
		for i := 0; i < 24; i++ {
			mid := (lo + hi) / 2
			if _, _, _, fits := try(mid); fits {
				lo = mid
			} else {
				hi = mid
			}
		}
		r, g, b, _ = try(lo)
	}
	enc := func(v float64) uint8 { return uint8(math.Round(linearToSrgb(clamp01(v)) * 255)) }
	return rgba{enc(r), enc(g), enc(b), alpha}
}

// luminance is the WCAG relative luminance.
func luminance(c rgba) float64 {
	return 0.2126*srgbToLinear(float64(c.r)/255) +
		0.7152*srgbToLinear(float64(c.g)/255) +
		0.0722*srgbToLinear(float64(c.b)/255)
}

// contrast is the WCAG contrast ratio (1..21).
func contrast(a, b rgba) float64 {
	la, lb := luminance(a), luminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

// mapper moves colors from the page background they were drawn on (src) to the
// background of the other theme (tgt).
//
// Each color is described by its signed perceptual distance e from the source
// background (OKLab lightness; positive means "contrasts with the background").
// The same distance, shaped by gamma, is laid out on the other side of the target
// background: a tint that barely differs from white stays a faint step above the
// dark page, while black ink becomes light ink. Chroma is reduced for dark
// targets, where saturated colors glare, and ink-like colors are kept at least
// inkMin away from the page.
type mapper struct {
	srcL, tgtL float64
	dir        float64 // +1: target is darker than source, -1: lighter
	maxE, room float64
	gamma      float64
	chroma     float64
	inkMin     float64
}

func newMapper(src, tgt rgba, o options) *mapper {
	ls, lt := toLab(src).L, toLab(tgt).L
	m := &mapper{srcL: ls, tgtL: lt, gamma: o.gamma, chroma: o.chroma, inkMin: o.inkMin}
	if ls >= lt {
		m.dir, m.maxE, m.room = 1, ls, o.lmax-lt
	} else {
		m.dir, m.maxE, m.room = -1, 1-ls, lt-(1-o.lmax)
	}
	m.maxE = math.Max(m.maxE, 0.05)
	m.room = math.Max(m.room, 0.05)
	return m
}

// away is how far l lies from the target background, in the direction ink moves.
func (m *mapper) away(l float64) float64 { return m.dir * (l - m.tgtL) }

// at is the lightness at distance d from the target background.
func (m *mapper) at(d float64) float64 { return m.tgtL + m.dir*d }

func (m *mapper) apply(c rgba, ink bool) rgba {
	lab := toLab(c)
	en := m.dir * (m.srcL - lab.L) / m.maxE
	sign := 1.0
	if en < 0 { // on the far side of the background (e.g. white on a tinted page)
		sign, en = -1, -en
	}
	d := sign * m.room * math.Pow(en, m.gamma)
	if ink {
		d = math.Max(d, math.Min(m.inkMin, m.room))
	}
	cs := 1.0
	if m.dir > 0 {
		cs = m.chroma
	}
	return labToRGBA(oklab{m.at(d), lab.a * cs, lab.b * cs}, c.a)
}
