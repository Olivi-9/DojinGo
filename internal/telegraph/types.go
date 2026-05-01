package telegraph

import (
	"encoding/json"
	"math/rand"
)

type PageCreate struct {
	Title      string `json:"title"`
	Content    []Node `json:"content"`
	AuthorName string `json:"author_name,omitempty"`
	AuthorURL  string `json:"author_url,omitempty"`
}

type Page struct {
	Path        string `json:"path"`
	URL         string `json:"url"`
	Title       string `json:"title"`
	Description string `json:"description"`
	AuthorName  string `json:"author_name"`
	AuthorURL   string `json:"author_url"`
	Views       int    `json:"views"`
}

type MediaInfo struct {
	Src string `json:"src"`
}

type Node struct {
	Tag      string `json:"tag,omitempty"`
	Attrs    *Attrs `json:"attrs,omitempty"`
	Children []Node `json:"children,omitempty"`
	Text     string `json:"-"`
	IsText   bool   `json:"-"`
}

type Attrs struct {
	Href string `json:"href,omitempty"`
	Src  string `json:"src,omitempty"`
}

func (n Node) MarshalJSON() ([]byte, error) {
	if n.IsText {
		return json.Marshal(n.Text)
	}
	type alias Node
	return json.Marshal(alias(n))
}

func TextNode(text string) Node {
	return Node{Text: text, IsText: true}
}

func Paragraph(children ...Node) Node {
	return Node{Tag: "p", Children: children}
}

func Link(href string, children ...Node) Node {
	return Node{Tag: "a", Attrs: &Attrs{Href: href}, Children: children}
}

func Image(src string) Node {
	return Node{Tag: "img", Attrs: &Attrs{Src: src}}
}

func (n Node) EstimateSize() int {
	if n.IsText {
		return len(n.Text)
	}
	size := 21 + len(n.Tag)
	if n.Attrs != nil {
		size += 11
		size += len(n.Attrs.Href) + len(n.Attrs.Src) + 18
	}
	if len(n.Children) > 0 {
		size += 14
		for _, child := range n.Children {
			size += child.EstimateSize() + 1
		}
	}
	return size
}

func randInt(n int) int {
	return rand.Intn(n)
}
