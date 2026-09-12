package index

import (
	"bytes"
	"encoding/xml"
	"io"
)

// Minimal offset-tracking XML tree used by the Android navigation-graph
// extractor (Go replacement for roxmltree). Token boundaries come from
// xml.Decoder.InputOffset: for a StartElement the offset range spans the open
// tag, for the matching EndElement it ends after the closing tag — the same
// semantics as roxmltree's Node::range().

// xmlNode is one element in the decoded document.
type xmlNode struct {
	Name     xml.Name
	Attrs    []xml.Attr
	Children []*xmlNode // element children only
	Parent   *xmlNode
	Start    int64 // byte offset of the open tag
	End      int64 // byte offset after the matching close tag
}

// Elements returns the element children.
func (n *xmlNode) Elements() []*xmlNode { return n.Children }

// xmlDoc is the decoded document with its root element.
type xmlDoc struct {
	Root *xmlNode
}

// parseXML decodes a small XML document into an element tree.
func parseXML(src []byte) (*xmlDoc, error) {
	dec := xml.NewDecoder(bytes.NewReader(src))
	doc := &xmlDoc{}
	var stack []*xmlNode
	prev := int64(0)

	for {
		tokStart := prev
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		tokEnd := dec.InputOffset()
		prev = tokEnd

		switch t := tok.(type) {
		case xml.StartElement:
			node := &xmlNode{Name: t.Name, Attrs: append([]xml.Attr(nil), t.Attr...), Start: tokStart}
			if len(stack) > 0 {
				parent := stack[len(stack)-1]
				node.Parent = parent
				parent.Children = append(parent.Children, node)
			}
			stack = append(stack, node)
		case xml.EndElement:
			if len(stack) == 0 {
				return nil, io.ErrUnexpectedEOF
			}
			top := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			top.End = tokEnd
			if len(stack) == 0 && doc.Root == nil {
				doc.Root = top
			}
		}
	}
	if doc.Root == nil && len(stack) > 0 {
		doc.Root = stack[0]
	}
	return doc, nil
}

// xmlAttrValue returns the value of attrName in the given namespace URI (or,
// when encoding/xml leaves an undeclared prefix unresolved, with the bare
// prefix as Space). Empty string when absent.
func xmlAttrValue(n *xmlNode, ns, local string) string {
	for _, a := range n.Attrs {
		if a.Name.Local != local {
			continue
		}
		if a.Name.Space == ns || a.Name.Space == prefixOfNS(ns) {
			return a.Value
		}
	}
	return ""
}

// prefixOfNS maps the two Android namespaces to their conventional prefixes,
// covering fixtures where encoding/xml cannot resolve the prefix.
func prefixOfNS(ns string) string {
	if ns == nsAndroid {
		return "android"
	}
	if ns == nsApp {
		return "app"
	}
	return ""
}

// androidXMLAttr returns android:<name> on the node ("" when absent).
func androidXMLAttr(n *xmlNode, ns, name string) string {
	return xmlAttrValue(n, ns, name)
}

// optionalXMLAttr returns the attribute value as any (nil when absent),
// mirroring the Rust Option<String> JSON serialization.
func optionalXMLAttr(n *xmlNode, ns, name string) any {
	if v := xmlAttrValue(n, ns, name); v != "" {
		return v
	}
	return nil
}

// optionalStrippedID returns the @+id/@id-stripped value of an attribute as
// any (nil when absent) — parity with the Rust android_id Option<String>.
func optionalStrippedID(n *xmlNode, ns, name string) any {
	if v := xmlAttrValue(n, ns, name); v != "" {
		return stripIDPrefix(v)
	}
	return nil
}

// stripIDPrefix removes the "@+id/" or "@id/" prefix from an Android
// resource reference.
func stripIDPrefix(s string) string {
	switch {
	case len(s) > 5 && s[:5] == "@+id/":
		return s[5:]
	case len(s) > 4 && s[:4] == "@id/":
		return s[4:]
	default:
		return s
	}
}
