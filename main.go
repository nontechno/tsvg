// svgtheme makes an SVG follow the viewer's light/dark color scheme.
//
// It parses the SVG, collects every unique color (explicit and implicit),
// defines one CSS custom property per color inside a <style> block with a
// prefers-color-scheme light set and a dark set, and rewrites the colors in the
// drawing to var(--name, original). The generated theme is derived from each
// color's perceptual distance to the page background, and text/background
// contrast is checked per theme.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"text/tabwriter"
)

func main() {
	opt := defaultOptions()
	var outPath, mapPath string
	var quiet, classic bool
	flag.StringVar(&outPath, "o", "", "output file (default <input>.themed.svg, '-' for stdout)")
	flag.StringVar(&opt.prefix, "prefix", opt.prefix, "prefix for variable names, e.g. 'svg-'")
	flag.StringVar(&opt.base, "base", opt.base, "theme the input was drawn for: 'light' or 'dark'")
	flag.StringVar(&opt.lightBG, "light-bg", opt.lightBG, "page background of the light theme")
	flag.StringVar(&opt.darkBG, "dark-bg", opt.darkBG, "page background of the dark theme")
	flag.BoolVar(&opt.paintBG, "bg", false, "also paint the page background (standalone SVG only)")
	flag.Float64Var(&opt.lmax, "lmax", opt.lmax, "lightness (OKLab 0..1) of the most contrasting ink in the generated theme")
	flag.Float64Var(&opt.gamma, "gamma", opt.gamma, "distance curve: <1 lifts faint tints away from the page, 1 is linear")
	flag.Float64Var(&opt.chroma, "chroma", opt.chroma, "chroma factor for a generated dark theme (1 keeps saturation)")
	flag.Float64Var(&opt.inkMin, "ink", opt.inkMin, "minimum lightness distance of text/line colors from the page")
	flag.Float64Var(&opt.minRatio, "min-ratio", opt.minRatio, "WCAG contrast threshold for text over its background")
	flag.BoolVar(&opt.fixContrast, "fix-contrast", false, "nudge the generated theme until text meets -min-ratio")
	flag.StringVar(&mapPath, "map", "", "color map file: lines of 'FROM TO' (a color in the SVG, its color in the generated theme); '# ' and '//' start comments")
	flag.BoolVar(&classic, "classic", false, "plain lightness inversion (-gamma 1 -chroma 1 -ink 0)")
	flag.BoolVar(&quiet, "q", false, "suppress the report")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: %s [flags] input.svg\n\n", filepath.Base(os.Args[0]))
		flag.PrintDefaults()
	}

	// Accept flags before and after the input file.
	var files []string
	for args := os.Args[1:]; len(args) > 0; {
		flag.CommandLine.Parse(args)
		args = flag.Args()
		if len(args) > 0 {
			files, args = append(files, args[0]), args[1:]
		}
	}
	if len(files) != 1 {
		flag.Usage()
		os.Exit(2)
	}
	if classic {
		opt.classic()
	}
	switch {
	case opt.base != "light" && opt.base != "dark":
		die("-base must be 'light' or 'dark'")
	case opt.lmax < 0.5 || opt.lmax > 1:
		die("-lmax must be between 0.5 and 1")
	case opt.gamma <= 0 || opt.gamma > 2:
		die("-gamma must be in (0, 2]")
	case opt.chroma < 0 || opt.chroma > 2:
		die("-chroma must be between 0 and 2")
	case opt.inkMin < 0 || opt.inkMin > 1:
		die("-ink must be between 0 and 1")
	case opt.minRatio < 1 || opt.minRatio > 21:
		die("-min-ratio must be between 1 and 21")
	case !regexp.MustCompile(`^[A-Za-z0-9_-]*$`).MatchString(opt.prefix):
		die("-prefix may only contain letters, digits, '-' and '_'")
	}

	if mapPath != "" {
		text, err := os.ReadFile(mapPath)
		if err != nil {
			die(err.Error())
		}
		if opt.colorMap, opt.mapWarnings, err = parseColorMap(string(text)); err != nil {
			die(mapPath + ": " + err.Error())
		}
	}

	in := files[0]
	src, err := os.ReadFile(in)
	if err != nil {
		die(err.Error())
	}
	res, err := transform(string(src), opt)
	if errors.Is(err, errAlreadyThemed) {
		die(in + ": " + err.Error() + "; run svgtheme on the original file instead")
	}
	if err != nil {
		die(in + ": " + err.Error())
	}

	if outPath == "" {
		ext := filepath.Ext(in)
		outPath = strings.TrimSuffix(in, ext) + ".themed" + ext
	}
	if outPath == "-" {
		io.WriteString(os.Stdout, res.out)
	} else if err := os.WriteFile(outPath, []byte(res.out), 0o644); err != nil {
		die(err.Error())
	}
	if !quiet {
		report(os.Stderr, in, outPath, res)
	}
}

func die(msg string) {
	fmt.Fprintln(os.Stderr, "svgtheme:", msg)
	os.Exit(1)
}

func report(w io.Writer, in, out string, res *result) {
	fmt.Fprintf(w, "%s -> %s: %d unique colors\n\n", in, out, len(res.palette))
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "VARIABLE\tLIGHT\tDARK\tEXPLICIT\tIMPLICIT\tUSED AS")
	anyPinned := false
	for _, ci := range res.palette {
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
		for _, r := range rs {
			parts = append(parts, fmt.Sprintf("%s×%d", r.role, r.n))
		}
		light, dark := ci.light, ci.dark
		if ci.pinned { // mark values that came from the color map
			if res.genTheme == "dark" {
				dark += "*"
			} else {
				light += "*"
			}
			anyPinned = true
		}
		fmt.Fprintf(tw, "--%s\t%s\t%s\t%d\t%d\t%s\n", ci.name, light, dark, ci.explicit, ci.implicit, strings.Join(parts, " "))
	}
	tw.Flush()
	if anyPinned {
		fmt.Fprintln(w, "* from the color map")
	}

	if res.texts > 0 {
		low, lowTexts := 0, 0
		for _, p := range res.pairs {
			if p.low {
				low++
				lowTexts += p.count
			}
		}
		fmt.Fprintf(w, "\ncontrast (text over what is behind it, WCAG, threshold %.1f:1): %d text elements checked, %d below threshold in the %s theme\n",
			res.minRatio, res.texts, lowTexts, res.genTheme)
		if low > 0 {
			tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
			fmt.Fprintln(tw, "TEXT COLOR\tON\tLIGHT\tDARK\tCOUNT")
			shown := 0
			for _, p := range res.pairs {
				if !p.low || shown == 10 {
					continue
				}
				shown++
				fmt.Fprintf(tw, "%s\t%s\t%.1f\t%.1f\t%d\n", p.fg, p.bg, p.light, p.dark, p.count)
			}
			tw.Flush()
		}
	}
	if len(res.adjustments) > 0 {
		fmt.Fprintln(w, "\nadjusted for contrast:")
		for _, a := range res.adjustments {
			fmt.Fprintln(w, "  "+a)
		}
	}
	for _, m := range res.warnings {
		fmt.Fprintln(w, "\nwarning:", m)
	}
}
