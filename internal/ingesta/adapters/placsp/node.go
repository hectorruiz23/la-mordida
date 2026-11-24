package placsp

import (
	"encoding/xml"
	"fmt"
	"strings"
)

// The namespaces are compared in full, never by the prefix a document happens
// to bind. A feed may rename its prefixes without changing the meaning of an
// element.
const (
	nsAtom      = "http://www.w3.org/2005/Atom"
	nsTombstone = "http://purl.org/atompub/tombstones/1.0"

	nsCAC = "urn:dgpe:names:draft:codice:schema:xsd:CommonAggregateComponents-2"
	nsCBC = "urn:dgpe:names:draft:codice:schema:xsd:CommonBasicComponents-2"

	nsCACExt = "urn:dgpe:names:draft:codice-place-ext:schema:xsd:CommonAggregateComponents-2"
	nsCBCExt = "urn:dgpe:names:draft:codice-place-ext:schema:xsd:CommonBasicComponents-2"
)

const maxDepth = 256

// node is the in-memory shape of one entry. Only the entry being translated is
// held, never the page or the archive: the decoder streams the document.
type node struct {
	space    string
	local    string
	attrs    []xml.Attr
	text     strings.Builder
	children []*node
}

// decodeNode consumes exactly the element that starts at start. Children are
// decoded recursively by token, so no prefix and no whole document is assumed.
func decodeNode(decoder *xml.Decoder, start xml.StartElement, depth int) (*node, error) {
	if depth > maxDepth {
		return nil, fmt.Errorf("placsp: <%s> nests deeper than %d levels", start.Name.Local, maxDepth)
	}

	current := &node{space: start.Name.Space, local: start.Name.Local, attrs: start.Attr}
	for {
		token, err := decoder.Token()
		if err != nil {
			return nil, fmt.Errorf("placsp: decode <%s>: %w", current.local, err)
		}
		switch value := token.(type) {
		case xml.StartElement:
			child, err := decodeNode(decoder, value, depth+1)
			if err != nil {
				return nil, err
			}
			current.children = append(current.children, child)
		case xml.CharData:
			current.text.Write(value)
		case xml.EndElement:
			return current, nil
		}
	}
}

func (n *node) value() string {
	if n == nil {
		return ""
	}
	return strings.TrimSpace(n.text.String())
}

func (n *node) attr(local string) string {
	if n == nil {
		return ""
	}
	for _, attribute := range n.attrs {
		if attribute.Name.Local == local {
			return strings.TrimSpace(attribute.Value)
		}
	}
	return ""
}

func (n *node) child(space, local string) *node {
	if n == nil {
		return nil
	}
	for _, candidate := range n.children {
		if candidate.space == space && candidate.local == local {
			return candidate
		}
	}
	return nil
}

func (n *node) childrenOf(space, local string) []*node {
	if n == nil {
		return nil
	}
	var result []*node
	for _, candidate := range n.children {
		if candidate.space == space && candidate.local == local {
			result = append(result, candidate)
		}
	}
	return result
}

// path walks a chain of namespaces and local names without assuming that
// intermediate elements exist.
func (n *node) path(steps ...step) *node {
	current := n
	for _, next := range steps {
		if current == nil {
			return nil
		}
		current = current.child(next.space, next.local)
	}
	return current
}

type step struct {
	space string
	local string
}

func at(space, local string) step { return step{space: space, local: local} }
