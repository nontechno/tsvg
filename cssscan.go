package main

import "strings"

// decl is one CSS declaration with the absolute position of its value in the file.
type decl struct {
	prop   string // lower-case property name
	value  string // trimmed value text, without !important
	vStart int    // absolute byte offset of value[0] in the source file
}

type rule struct {
	sels  []string
	decls []decl
	order int
}

func isSpace(b byte) bool      { return b == ' ' || b == '\t' || b == '\n' || b == '\r' || b == '\f' }
func isDigit(b byte) bool      { return b >= '0' && b <= '9' }
func isLetter(b byte) bool     { return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' }
func isIdentStart(b byte) bool { return isLetter(b) || b == '_' || b == '-' }
func isIdentChar(b byte) bool  { return isIdentStart(b) || isDigit(b) }

// skipString returns the index just past the string that starts at s[i].
func skipString(s string, i int) int {
	q := s[i]
	for i++; i < len(s); i++ {
		switch s[i] {
		case '\\':
			i++
		case q:
			return i + 1
		}
	}
	return len(s)
}

// skipComment returns the index just past the /* */ comment starting at s[i].
func skipComment(s string, i int) int {
	j := strings.Index(s[i+2:], "*/")
	if j < 0 {
		return len(s)
	}
	return i + 2 + j + 2
}

func isCommentStart(s string, i int) bool { return s[i] == '/' && i+1 < len(s) && s[i+1] == '*' }

// matchParen: v[open] == '('. Returns the index just past the matching ')'.
func matchParen(v string, open int) (int, bool) {
	depth := 0
	for i := open; i < len(v); i++ {
		switch v[i] {
		case '"', '\'':
			i = skipString(v, i) - 1
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i + 1, true
			}
		}
	}
	return len(v), false
}

// matchBrace: s[open] == '{'. Returns the index of the matching '}' (or len(s)).
func matchBrace(s string, open int) int {
	depth := 0
	for i := open; i < len(s); {
		switch {
		case isCommentStart(s, i):
			i = skipComment(s, i)
		case s[i] == '"' || s[i] == '\'':
			i = skipString(s, i)
		case s[i] == '{':
			depth++
			i++
		case s[i] == '}':
			depth--
			if depth == 0 {
				return i
			}
			i++
		default:
			i++
		}
	}
	return len(s)
}

// parseDecls splits "a: b; c: d" into declarations. base is the absolute offset of text[0].
func parseDecls(text string, base int) []decl {
	var out []decl
	segStart := 0
	flush := func(end int) {
		seg := text[segStart:end]
		colon := strings.IndexByte(seg, ':')
		if colon <= 0 {
			return
		}
		prop := strings.ToLower(strings.TrimSpace(seg[:colon]))
		raw := seg[colon+1:]
		lead := len(raw) - len(strings.TrimLeft(raw, " \t\r\n\f"))
		val := strings.TrimSpace(raw)
		if strings.HasSuffix(strings.ToLower(val), "!important") {
			val = strings.TrimSpace(val[:len(val)-len("!important")])
		}
		if prop != "" && val != "" {
			out = append(out, decl{prop, val, base + segStart + colon + 1 + lead})
		}
	}
	depth := 0
	for i := 0; i < len(text); {
		switch {
		case isCommentStart(text, i):
			i = skipComment(text, i)
		case text[i] == '"' || text[i] == '\'':
			i = skipString(text, i)
		case text[i] == '(':
			depth++
			i++
		case text[i] == ')':
			if depth > 0 {
				depth--
			}
			i++
		case text[i] == ';' && depth == 0:
			flush(i)
			segStart = i + 1
			i++
		default:
			i++
		}
	}
	flush(len(text))
	return out
}

func stripComments(s string) string {
	for {
		i := strings.Index(s, "/*")
		if i < 0 {
			return s
		}
		s = s[:i] + " " + s[skipComment(s, i):]
	}
}

func splitSelectors(s string) []string {
	var out []string
	depth, start := 0, 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '(', '[':
			depth++
		case ')', ']':
			depth--
		case ',':
			if depth == 0 {
				out = append(out, strings.TrimSpace(s[start:i]))
				start = i + 1
			}
		}
	}
	return append(out, strings.TrimSpace(s[start:]))
}

// maskCDATA blanks the CDATA delimiters, keeping every byte offset unchanged.
func maskCDATA(s string) string {
	s = strings.ReplaceAll(s, "<![CDATA[", strings.Repeat(" ", len("<![CDATA[")))
	return strings.ReplaceAll(s, "]]>", "   ")
}

// parseCSS walks a stylesheet and appends its style rules to out. Nested at-rules
// (@media, @supports, @keyframes, ...) are descended into, except @media blocks
// that already target prefers-color-scheme, which are left untouched and counted.
func parseCSS(text string, base int, out *[]rule, skippedDark *int) {
	start := 0
	for i := 0; i < len(text); {
		switch {
		case isCommentStart(text, i):
			i = skipComment(text, i)
		case text[i] == '"' || text[i] == '\'':
			i = skipString(text, i)
		case text[i] == ';': // statement at-rule such as @import
			start = i + 1
			i++
		case text[i] == '{':
			prelude := strings.TrimSpace(stripComments(text[start:i]))
			end := matchBrace(text, i)
			body, bodyBase := text[i+1:min(end, len(text))], base+i+1
			if strings.HasPrefix(prelude, "@") {
				lp := strings.ToLower(prelude)
				switch {
				case strings.HasPrefix(lp, "@media") && strings.Contains(lp, "prefers-color-scheme"):
					*skippedDark++
				case strings.HasPrefix(lp, "@font-face"), strings.HasPrefix(lp, "@page"):
				default:
					parseCSS(body, bodyBase, out, skippedDark)
				}
			} else {
				*out = append(*out, rule{
					sels:  splitSelectors(prelude),
					decls: parseDecls(body, bodyBase),
					order: len(*out),
				})
			}
			i = end + 1
			start = i
		default:
			i++
		}
	}
}

// selectorSupported reports whether matchSelector can evaluate sel
// (a compound of type, .class and #id, no combinators or pseudo-classes).
func selectorSupported(sel string) bool {
	return sel != "" && !strings.ContainsAny(sel, " >+~[:\t\r\n")
}

// matchSelector matches simple selectors against an element and returns the specificity.
func matchSelector(sel string, e *elem) (spec int, ok bool) {
	if !selectorSupported(sel) {
		return 0, false
	}
	i := 0
	for i < len(sel) && (sel[i] == '*' || isIdentChar(sel[i])) {
		i++
	}
	if typ := sel[:i]; typ != "" && typ != "*" {
		if typ != e.name {
			return 0, false
		}
		spec++
	}
	classes := strings.Fields(e.attrVal("class"))
	for i < len(sel) {
		kind := sel[i]
		j := i + 1
		for j < len(sel) && isIdentChar(sel[j]) {
			j++
		}
		name := sel[i+1 : j]
		switch kind {
		case '.':
			found := false
			for _, c := range classes {
				if c == name {
					found = true
				}
			}
			if !found {
				return 0, false
			}
			spec += 10
		case '#':
			if e.attrVal("id") != name {
				return 0, false
			}
			spec += 100
		default:
			return 0, false
		}
		i = j
	}
	return spec, true
}

// colorTok is a color found inside a CSS value; s and e are offsets within that value.
type colorTok struct {
	s, e int
	c    rgba
}

// scanColors finds hex, rgb()/hsl() and named colors in a CSS value. url(), var()
// and other opaque functions are skipped; currentColor, transparent and the like
// are not themeable and never reported. Color functions that cannot be handled are
// reported through warn.
func scanColors(v string, warn func(string)) []colorTok {
	var out []colorTok
	n := len(v)
	for i := 0; i < n; {
		ch := v[i]
		switch {
		case ch == '"' || ch == '\'':
			i = skipString(v, i)
		case ch == '#':
			j := i + 1
			for j < n && isHexDigit(v[j]) {
				j++
			}
			if j < n && isIdentChar(v[j]) { // e.g. an id reference, not a color
				for j < n && isIdentChar(v[j]) {
					j++
				}
			} else if c, ok := parseHex(v[i+1 : j]); ok {
				out = append(out, colorTok{i, j, c})
			}
			i = j
		case isDigit(ch):
			j := i + 1
			for j < n && isIdentChar(v[j]) {
				j++
			}
			i = j
		case isIdentStart(ch):
			j := i + 1
			for j < n && isIdentChar(v[j]) {
				j++
			}
			word := strings.ToLower(v[i:j])
			if j < n && v[j] == '(' {
				end, ok := matchParen(v, j)
				switch word {
				case "rgb", "rgba", "hsl", "hsla":
					if !ok {
						warn("unbalanced color function: " + v[i:])
					} else if c, parsed := parseColorFunc(word, v[j+1:end-1]); parsed {
						out = append(out, colorTok{i, end, c})
					} else {
						warn("could not parse color: " + v[i:end])
					}
					i = end
				case "url", "var", "env", "attr", "calc":
					i = end
				case "hwb", "lab", "lch", "oklab", "oklch", "color", "color-mix", "light-dark":
					warn("unsupported color function left unchanged: " + word + "()")
					i = end
				default: // drop-shadow(), linear-gradient(), ...: look inside
					i = j + 1
				}
				continue
			}
			if c, ok := namedColors[word]; ok {
				out = append(out, colorTok{i, j, c})
			}
			i = j
		default:
			i++
		}
	}
	return out
}

// colorRole maps a CSS property to a short role name used in variable names,
// or reports false if the property does not carry themeable colors.
func colorRole(prop string, textElem bool) (string, bool) {
	switch {
	case prop == "fill":
		if textElem {
			return "text", true
		}
		return "fill", true
	case prop == "stroke":
		return "stroke", true
	case prop == "stop-color":
		return "stop", true
	case prop == "flood-color":
		return "flood", true
	case prop == "lighting-color":
		return "lighting", true
	case prop == "color":
		return "color", true
	case prop == "background" || prop == "background-color":
		return "bg", true
	case strings.HasPrefix(prop, "border"), strings.HasPrefix(prop, "outline"), strings.HasPrefix(prop, "column-rule"):
		return "border", true
	case prop == "box-shadow" || prop == "text-shadow":
		return "shadow", true
	case strings.HasPrefix(prop, "text-decoration"), strings.HasPrefix(prop, "-webkit-text-stroke"),
		prop == "caret-color", prop == "accent-color", prop == "text-emphasis-color":
		return "color", true
	}
	return "", false
}
