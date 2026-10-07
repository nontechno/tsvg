package main

import (
	"encoding/xml"
	"fmt"
	"io"
	"strings"
)

// attr is one attribute with the absolute position of its raw value text.
type attr struct {
	name   string
	val    string
	vStart int
}

// elem is a start tag found in the source, with its byte range [start, end).
type elem struct {
	name       string
	attrs      []attr
	parent     *elem
	start, end int
	selfClose  bool

	inForeign bool // inside <foreignObject> (HTML, not SVG painting)
	skip      bool // inside <title>, <desc>, <metadata>, <script>, <style>
	inClip    bool // inside <clipPath>: paint is irrelevant

	styleDecls []decl            // from the style="" attribute
	ruleVal    map[string]string // winning stylesheet value per property
	ruleKey    map[string][2]int // (specificity, order) of that winner
}

func (e *elem) attrOf(name string) *attr {
	for i := range e.attrs {
		if e.attrs[i].name == name {
			return &e.attrs[i]
		}
	}
	return nil
}

func (e *elem) attrVal(name string) string {
	if a := e.attrOf(name); a != nil {
		return a.val
	}
	return ""
}

// own returns the value this element itself specifies for prop, in CSS priority
// order: style attribute, then stylesheet rules, then presentation attribute.
func (e *elem) own(prop string) (string, bool) {
	val, found := "", false
	for _, d := range e.styleDecls {
		if d.prop == prop {
			val, found = d.value, true
		}
	}
	if !found {
		val, found = e.ruleVal[prop]
	}
	if !found {
		if a := e.attrOf(prop); a != nil {
			val, found = a.val, true
		}
	}
	val = strings.TrimSpace(val)
	if !found || val == "" || strings.EqualFold(val, "inherit") {
		return "", false
	}
	return val, true
}

// effFill returns the fill in effect for e, following inheritance.
func effFill(e *elem) (string, bool) {
	for x := e; x != nil; x = x.parent {
		if v, ok := x.own("fill"); ok {
			return v, true
		}
	}
	return "", false
}

type styleBlock struct{ start, end int } // raw text range between <style> and </style>

type document struct {
	elems          []*elem
	root           *elem
	styles         []styleBlock
	xmlStyleSheets []string
	themed         bool // already carries our generated <style>
}

var skipNames = map[string]bool{"title": true, "desc": true, "metadata": true, "script": true, "style": true}

// scanSVG tokenizes src with encoding/xml only to find tag boundaries; all edits are
// made on the original text so the rest of the file is preserved byte for byte.
func scanSVG(src string) (*document, error) {
	dec := xml.NewDecoder(strings.NewReader(src))
	dec.Strict = false
	dec.CharsetReader = func(_ string, r io.Reader) (io.Reader, error) { return r, nil }

	doc := &document{}
	var stack []*elem
	prev, styleOpen := 0, -1
	skipEnd := false
	for {
		tok, err := dec.RawToken()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("parsing SVG: %w", err)
		}
		cur := int(dec.InputOffset())
		switch t := tok.(type) {
		case xml.StartElement:
			e := &elem{name: t.Name.Local, start: prev, end: cur}
			e.attrs, e.selfClose = parseTag(src, prev, cur)
			if len(stack) > 0 {
				p := stack[len(stack)-1]
				e.parent = p
				e.inForeign = p.inForeign || p.name == "foreignObject"
				e.skip = p.skip
				e.inClip = p.inClip || p.name == "clipPath"
			} else if doc.root == nil {
				doc.root = e
			}
			if skipNames[strings.ToLower(e.name)] {
				e.skip = true
			}
			if e.name == "style" {
				if e.attrVal("id") == markerID {
					doc.themed = true
				}
				if !e.selfClose {
					styleOpen = cur
				}
			}
			doc.elems = append(doc.elems, e)
			if e.selfClose {
				skipEnd = true // RawToken reports a synthetic EndElement next
			} else {
				stack = append(stack, e)
			}
		case xml.EndElement:
			if skipEnd {
				skipEnd = false
				break
			}
			if t.Name.Local == "style" && styleOpen >= 0 {
				doc.styles = append(doc.styles, styleBlock{styleOpen, prev})
				styleOpen = -1
			}
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		case xml.ProcInst:
			if t.Target == "xml-stylesheet" {
				doc.xmlStyleSheets = append(doc.xmlStyleSheets, string(t.Inst))
			}
		}
		prev = cur
	}
	return doc, nil
}

// parseTag extracts the attributes of the start tag src[start:end] together with
// the absolute offset of each raw value.
func parseTag(src string, start, end int) ([]attr, bool) {
	raw := src[start:end]
	selfClose := strings.HasSuffix(raw, "/>")
	n := len(raw)
	i := 1 // skip '<'
	for i < n && !isSpace(raw[i]) && raw[i] != '/' && raw[i] != '>' {
		i++
	}
	var attrs []attr
	for i < n {
		for i < n && isSpace(raw[i]) {
			i++
		}
		if i >= n || raw[i] == '>' || raw[i] == '/' {
			break
		}
		ns := i
		for i < n && !isSpace(raw[i]) && raw[i] != '=' && raw[i] != '>' && raw[i] != '/' {
			i++
		}
		name := raw[ns:i]
		for i < n && isSpace(raw[i]) {
			i++
		}
		if i >= n || raw[i] != '=' {
			attrs = append(attrs, attr{name: name, vStart: start + i})
			continue
		}
		i++
		for i < n && isSpace(raw[i]) {
			i++
		}
		if i >= n {
			break
		}
		if q := raw[i]; q == '"' || q == '\'' {
			i++
			vs := i
			for i < n && raw[i] != q {
				i++
			}
			attrs = append(attrs, attr{name, raw[vs:i], start + vs})
			i++
		} else {
			vs := i
			for i < n && !isSpace(raw[i]) && raw[i] != '>' {
				i++
			}
			attrs = append(attrs, attr{name, raw[vs:i], start + vs})
		}
	}
	return attrs, selfClose
}
