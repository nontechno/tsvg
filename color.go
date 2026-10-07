package main

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// rgba is an 8-bit sRGB color with 8-bit alpha.
type rgba struct{ r, g, b, a uint8 }

// hex returns #rrggbb, or #rrggbbaa when the color is not fully opaque.
func (c rgba) hex() string {
	if c.a == 255 {
		return fmt.Sprintf("#%02x%02x%02x", c.r, c.g, c.b)
	}
	return fmt.Sprintf("#%02x%02x%02x%02x", c.r, c.g, c.b, c.a)
}

// hexRGB returns rrggbb without '#' and without alpha.
func (c rgba) hexRGB() string { return fmt.Sprintf("%02x%02x%02x", c.r, c.g, c.b) }

func isHexDigit(b byte) bool {
	return b >= '0' && b <= '9' || b >= 'a' && b <= 'f' || b >= 'A' && b <= 'F'
}

// parseHex parses the digits of #rgb, #rgba, #rrggbb or #rrggbbaa (without '#').
func parseHex(s string) (rgba, bool) {
	for i := 0; i < len(s); i++ {
		if !isHexDigit(s[i]) {
			return rgba{}, false
		}
	}
	v := [4]uint8{0, 0, 0, 255}
	switch len(s) {
	case 3, 4:
		for i := 0; i < len(s); i++ {
			n, _ := strconv.ParseUint(s[i:i+1], 16, 8)
			v[i] = uint8(n * 17)
		}
	case 6, 8:
		for i := 0; i < len(s)/2; i++ {
			n, _ := strconv.ParseUint(s[2*i:2*i+2], 16, 8)
			v[i] = uint8(n)
		}
	default:
		return rgba{}, false
	}
	return rgba{v[0], v[1], v[2], v[3]}, true
}

func clamp01(v float64) float64 { return math.Max(0, math.Min(1, v)) }

// parseScaled parses "50%" (as 0.5*scale) or a plain number (as is).
func parseScaled(tok string, scale float64) (float64, bool) {
	if p, ok := strings.CutSuffix(tok, "%"); ok {
		f, err := strconv.ParseFloat(p, 64)
		if err != nil {
			return 0, false
		}
		return f / 100 * scale, true
	}
	f, err := strconv.ParseFloat(tok, 64)
	return f, err == nil
}

// parsePct parses a saturation/lightness token ("50%" or "50") into 0..1.
func parsePct(tok string) (float64, bool) {
	tok = strings.TrimSuffix(tok, "%")
	f, err := strconv.ParseFloat(tok, 64)
	return f / 100, err == nil
}

// parseHue parses a hue with an optional deg/rad/grad/turn unit into degrees.
func parseHue(tok string) (float64, bool) {
	mult := 1.0
	for suffix, m := range map[string]float64{"deg": 1, "grad": 0.9, "rad": 180 / math.Pi, "turn": 360} {
		if p, ok := strings.CutSuffix(tok, suffix); ok {
			tok, mult = p, m
			break
		}
	}
	f, err := strconv.ParseFloat(tok, 64)
	return f * mult, err == nil
}

func hslToRGB(h, s, l float64) (float64, float64, float64) {
	h = math.Mod(h, 360)
	if h < 0 {
		h += 360
	}
	s, l = clamp01(s), clamp01(l)
	c := (1 - math.Abs(2*l-1)) * s
	x := c * (1 - math.Abs(math.Mod(h/60, 2)-1))
	m := l - c/2
	var r, g, b float64
	switch {
	case h < 60:
		r, g, b = c, x, 0
	case h < 120:
		r, g, b = x, c, 0
	case h < 180:
		r, g, b = 0, c, x
	case h < 240:
		r, g, b = 0, x, c
	case h < 300:
		r, g, b = x, 0, c
	default:
		r, g, b = c, 0, x
	}
	return (r + m) * 255, (g + m) * 255, (b + m) * 255
}

func toByte(v float64) uint8 { return uint8(math.Round(math.Max(0, math.Min(255, v)))) }

// parseColorFunc parses the arguments of rgb()/rgba()/hsl()/hsla().
func parseColorFunc(name, args string) (rgba, bool) {
	f := strings.Fields(strings.NewReplacer(",", " ", "/", " ").Replace(args))
	if len(f) != 3 && len(f) != 4 {
		return rgba{}, false
	}
	alpha := 1.0
	if len(f) == 4 {
		a, ok := parseScaled(f[3], 1)
		if !ok {
			return rgba{}, false
		}
		alpha = a
	}
	var r, g, b float64
	switch name {
	case "rgb", "rgba":
		var ok1, ok2, ok3 bool
		r, ok1 = parseScaled(f[0], 255)
		g, ok2 = parseScaled(f[1], 255)
		b, ok3 = parseScaled(f[2], 255)
		if !(ok1 && ok2 && ok3) {
			return rgba{}, false
		}
	case "hsl", "hsla":
		h, ok1 := parseHue(f[0])
		s, ok2 := parsePct(f[1])
		l, ok3 := parsePct(f[2])
		if !(ok1 && ok2 && ok3) {
			return rgba{}, false
		}
		r, g, b = hslToRGB(h, s, l)
	default:
		return rgba{}, false
	}
	return rgba{toByte(r), toByte(g), toByte(b), toByte(clamp01(alpha) * 255)}, true
}

// --- OKLab -----------------------------------------------------------------

type oklab struct{ L, a, b float64 }

func srgbToLinear(v float64) float64 {
	if v <= 0.04045 {
		return v / 12.92
	}
	return math.Pow((v+0.055)/1.055, 2.4)
}

func linearToSrgb(v float64) float64 {
	if v <= 0.0031308 {
		return 12.92 * v
	}
	return 1.055*math.Pow(v, 1/2.4) - 0.055
}

func toOKLab(r, g, b float64) oklab {
	l := 0.4122214708*r + 0.5363325363*g + 0.0514459929*b
	m := 0.2119034982*r + 0.6806995451*g + 0.1073969566*b
	s := 0.0883024619*r + 0.2817188376*g + 0.6299787005*b
	l, m, s = math.Cbrt(l), math.Cbrt(m), math.Cbrt(s)
	return oklab{
		0.2104542553*l + 0.7936177850*m - 0.0040720468*s,
		1.9779984951*l - 2.4285922050*m + 0.4505937099*s,
		0.0259040371*l + 0.7827717662*m - 0.8086757660*s,
	}
}

// fromOKLab returns linear (unclamped) sRGB.
func fromOKLab(c oklab) (r, g, b float64) {
	l := c.L + 0.3963377774*c.a + 0.2158037573*c.b
	m := c.L - 0.1055613458*c.a - 0.0638541728*c.b
	s := c.L - 0.0894841775*c.a - 1.2914855480*c.b
	l, m, s = l*l*l, m*m*m, s*s*s
	return 4.0767416621*l - 3.3077115913*m + 0.2309699292*s,
		-1.2684380046*l + 2.6097574011*m - 0.3413193965*s,
		-0.0041960863*l - 0.7034186147*m + 1.7076147010*s
}
