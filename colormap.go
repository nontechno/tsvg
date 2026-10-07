package main

import (
	"fmt"
	"strings"
)

// mapEntry is one line of a color map file.
type mapEntry struct {
	to   rgba
	line int
}

// stripComment removes a trailing comment. A comment starts with "//" or with a '#'
// that is followed by whitespace (or ends the line) and preceded by whitespace (or
// starts the line), so "#969696" is a color, not a comment.
func stripComment(s string) string {
	for i := 0; i < len(s); i++ {
		switch {
		case s[i] == '/' && i+1 < len(s) && s[i+1] == '/':
			return s[:i]
		case s[i] == '#' && (i == 0 || isSpace(s[i-1])) && (i+1 == len(s) || isSpace(s[i+1])):
			return s[:i]
		}
	}
	return s
}

// parseColorMap reads a color map: one "FROM TO" pair per line.
//
// FROM is a color as it appears in the SVG and TO is the color the generated theme
// uses for it. Either may use any CSS syntax (#rgb, #rrggbb, #rrggbbaa, a name,
// rgb(), hsl()). Colors are matched exactly, alpha included. Empty lines and
// comments are ignored; see stripComment.
func parseColorMap(text string) (m map[rgba]mapEntry, warnings []string, err error) {
	m = map[rgba]mapEntry{}
	for i, raw := range strings.Split(text, "\n") {
		n := i + 1
		s := strings.TrimSpace(stripComment(raw))
		if s == "" {
			continue
		}
		var problem string
		toks := scanColors(s, func(msg string) {
			if problem == "" {
				problem = msg
			}
		})
		switch {
		case problem != "":
			return nil, nil, fmt.Errorf("line %d: %s", n, problem)
		case len(toks) < 2:
			return nil, nil, fmt.Errorf("line %d: expected two colors (FROM TO), found %d in %q", n, len(toks), s)
		case toks[0].s != 0:
			return nil, nil, fmt.Errorf("line %d: a line must start with a color, found %q", n, s[:toks[0].s])
		case strings.TrimSpace(s[toks[0].e:toks[1].s]) != "":
			return nil, nil, fmt.Errorf("line %d: unexpected text %q between the colors", n, strings.TrimSpace(s[toks[0].e:toks[1].s]))
		case strings.TrimSpace(s[toks[1].e:]) != "":
			return nil, nil, fmt.Errorf("line %d: unexpected text %q after the second color (comments start with '# ' or '//')", n, strings.TrimSpace(s[toks[1].e:]))
		case toks[0].c.a == 0:
			return nil, nil, fmt.Errorf("line %d: fully transparent colors are never themed", n)
		}
		from := toks[0].c
		if prev, dup := m[from]; dup {
			warnings = append(warnings, fmt.Sprintf("color map line %d overrides line %d for %s", n, prev.line, from.hex()))
		}
		m[from] = mapEntry{to: toks[1].c, line: n}
	}
	return m, warnings, nil
}
