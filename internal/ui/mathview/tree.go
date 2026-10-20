// Package mathview shows LaTeX math with MyGo: display math laid out from
// the MathML that github.com/wyatt915/treeblood makes of it, following the
// layout algorithms of W3C MathML Core for the elements common in the
// answers of language models, and any math as readable Unicode text where
// it cannot be laid out (Unicode).
//
// Markdown shows Markdown with math in it: display math laid out, math
// within text as Unicode text, and copies the TeX the source was written
// with. The package knows nothing of the app around it.
package mathview

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/wyatt915/treeblood"
)

// Node is an element of MathML: its tag, attributes and children, or, for
// a token element (mi, mn, mo, mtext, mspace), its text.
type Node struct {
	Tag  string
	Attr map[string]string
	Text string
	Kids []*Node
}

// Get returns the attribute name, or "".
func (n *Node) Get(name string) string { return n.Attr[name] }

// macros are TeX commands treeblood does not know that answers use.
var macros = map[string]string{
	"operatorname": `\mathrm{#1}`,
}

// ErrUnsupported is wrapped by the errors of math that the layout does not
// support.
var ErrUnsupported = errors.New("unsupported math")

// Parse converts tex, without its delimiters, to MathML with treeblood and
// returns the MathML's content: the math element's, without its
// annotation.
func Parse(tex string) (n *Node, err error) {
	defer func() {
		if r := recover(); r != nil {
			n, err = nil, fmt.Errorf("treeblood: %v", r)
		}
	}()
	if strings.TrimSpace(tex) == "" {
		return nil, fmt.Errorf("%w: empty", ErrUnsupported)
	}
	mml, err := treeblood.TexToMML(tex, macros, true, false)
	if err != nil {
		return nil, err
	}
	root, err := ParseMathML(mml)
	if err != nil {
		return nil, err
	}
	return content(root), nil
}

// content returns what a math element shows: the first child of its
// semantics, the whole of it otherwise.
func content(n *Node) *Node {
	for n != nil && (n.Tag == "math" || n.Tag == "semantics") {
		var kids []*Node
		for _, k := range n.Kids {
			if k.Tag != "annotation" && k.Tag != "annotation-xml" {
				kids = append(kids, k)
			}
		}
		if len(kids) != 1 {
			return &Node{Tag: "mrow", Kids: kids}
		}
		n = kids[0]
	}
	return n
}

// entities are the entities treeblood writes beyond XML's.
var entities = map[string]string{
	"nbsp":             " ",
	"UnderBrace":       "⏟",
	"OverBrace":        "⏞",
	"UnderBracket":     "⎵",
	"OverBracket":      "⎴",
	"UnderParenthesis": "⏝",
	"OverParenthesis":  "⏜",
	"InvisibleTimes":   "⁢",
	"ApplyFunction":    "⁡",
}

// tokens are the elements whose content is text.
var tokens = map[string]bool{"mi": true, "mn": true, "mo": true, "mtext": true, "ms": true, "mspace": true, "merror": true, "none": true}

// ParseMathML reads MathML into a tree, leaving out annotations.
func ParseMathML(s string) (*Node, error) {
	// treeblood copies the TeX into the annotation as it is, ampersands
	// and all: it is not XML.
	for {
		i := strings.Index(s, "<annotation")
		if i < 0 {
			break
		}
		j := strings.Index(s[i:], "</annotation>")
		if j < 0 {
			s = s[:i]
			break
		}
		s = s[:i] + s[i+j+len("</annotation>"):]
	}
	d := xml.NewDecoder(strings.NewReader(s))
	d.Strict = false
	d.Entity = entities
	var stack []*Node
	var root *Node
	for {
		t, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		switch t := t.(type) {
		case xml.StartElement:
			n := &Node{Tag: t.Name.Local}
			for _, a := range t.Attr {
				if n.Attr == nil {
					n.Attr = map[string]string{}
				}
				n.Attr[a.Name.Local] = a.Value
			}
			if len(stack) > 0 {
				p := stack[len(stack)-1]
				p.Kids = append(p.Kids, n)
			} else if root == nil {
				root = n
			}
			stack = append(stack, n)
		case xml.EndElement:
			if len(stack) == 0 {
				return nil, errors.New("mathml: unbalanced end tag " + t.Name.Local)
			}
			n := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if tokens[n.Tag] && n.Tag != "mtext" {
				n.Text = strings.TrimSpace(n.Text)
			}
		case xml.CharData:
			if len(stack) == 0 {
				continue
			}
			n := stack[len(stack)-1]
			if tokens[n.Tag] {
				n.Text += string(t)
			} else if strings.TrimSpace(string(t)) != "" {
				// Text where elements belong, as treeblood leaves in a
				// table it could not read.
				n.Text += string(t)
			}
		}
	}
	if root == nil {
		return nil, errors.New("mathml: no element")
	}
	return root, nil
}

// supported are the elements the layout knows.
var supported = map[string]bool{
	"math": true, "semantics": true, "mrow": true, "mstyle": true, "mpadded": true,
	"mi": true, "mn": true, "mo": true, "mtext": true, "mspace": true,
	"mfrac": true, "msup": true, "msub": true, "msubsup": true,
	"msqrt": true, "mroot": true, "munder": true, "mover": true, "munderover": true,
	"mtable": true, "mtr": true, "mtd": true, "mphantom": true, "menclose": true,
}

// arity is how many children an element needs.
var arity = map[string]int{
	"mfrac": 2, "msup": 2, "msub": 2, "msubsup": 3, "mroot": 2,
	"munder": 2, "mover": 2, "munderover": 3,
}

// Check returns an error wrapping ErrUnsupported for the first element of n
// that the layout does not support, or nil.
func Check(n *Node) error {
	if n == nil {
		return fmt.Errorf("%w: nothing", ErrUnsupported)
	}
	if !supported[n.Tag] {
		if n.Tag == "merror" {
			return fmt.Errorf("%w: unknown command %q", ErrUnsupported, n.Text)
		}
		return fmt.Errorf("%w: <%s>", ErrUnsupported, n.Tag)
	}
	if !tokens[n.Tag] && strings.TrimSpace(n.Text) != "" {
		return fmt.Errorf("%w: text in <%s>", ErrUnsupported, n.Tag)
	}
	if k, ok := arity[n.Tag]; ok && len(n.Kids) != k {
		return fmt.Errorf("%w: <%s> with %d children", ErrUnsupported, n.Tag, len(n.Kids))
	}
	switch n.Tag {
	case "mspace":
		if n.Get("linebreak") != "" {
			return fmt.Errorf("%w: line break", ErrUnsupported)
		}
	case "mtable":
		for _, r := range n.Kids {
			if r.Tag != "mtr" {
				return fmt.Errorf("%w: <%s> in a table", ErrUnsupported, r.Tag)
			}
		}
	case "mtr":
		for _, c := range n.Kids {
			if c.Tag != "mtd" {
				return fmt.Errorf("%w: <%s> in a table row", ErrUnsupported, c.Tag)
			}
		}
	}
	for _, k := range n.Kids {
		if err := Check(k); err != nil {
			return err
		}
	}
	return nil
}
