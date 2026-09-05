// Package rss implements PAR-3.3 RSS/Atom intake: feeds are polled on their
// own goroutines (never blocking the torrent poller), items are deduplicated
// by GUID and enclosure hash, and ordered filters auto-load matching items
// with per-filter match history. Feed credentials are treated as secrets and
// never logged.
package rss

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// maxFeedBytes bounds a single feed fetch.
const maxFeedBytes = 8 << 20

// Enclosure is one downloadable payload attached to an item.
type Enclosure struct {
	URL    string
	Type   string
	Length int64 // -1 when unknown
}

// Item is one normalized feed entry.
type Item struct {
	ID         string // guid/id, else enclosure URL, else link
	Title      string
	Link       string
	Enclosure  Enclosure
	Categories []string
	Published  time.Time
}

// ParseFeed parses an RSS 2.0 or Atom document into normalized items.
func ParseFeed(data []byte) ([]Item, error) {
	kind, err := rootElement(data)
	if err != nil {
		return nil, err
	}
	switch kind {
	case "rss", "rdf":
		return parseRSS(data)
	case "feed":
		return parseAtom(data)
	default:
		return nil, fmt.Errorf("unsupported feed root element %q", kind)
	}
}

// rootElement returns the local name of the document's root element.
func rootElement(data []byte) (string, error) {
	dec := xml.NewDecoder(bytes.NewReader(data))
	for {
		tok, err := dec.Token()
		if err != nil {
			return "", fmt.Errorf("not a valid XML feed: %w", err)
		}
		if start, ok := tok.(xml.StartElement); ok {
			return start.Name.Local, nil
		}
	}
}

// --- RSS 2.0 ---

type rssDoc struct {
	Channel rssChannel `xml:"channel"`
}

type rssChannel struct {
	Items []rssItem `xml:"item"`
}

type rssItem struct {
	Title      string        `xml:"title"`
	Link       string        `xml:"link"`
	GUID       string        `xml:"guid"`
	Enclosure  *rssEnclosure `xml:"enclosure"`
	Categories []string      `xml:"category"`
	PubDate    string        `xml:"pubDate"`
}

type rssEnclosure struct {
	URL    string `xml:"url,attr"`
	Type   string `xml:"type,attr"`
	Length string `xml:"length,attr"`
}

func parseRSS(data []byte) ([]Item, error) {
	var doc rssDoc
	if err := xml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse RSS: %w", err)
	}
	out := make([]Item, 0, len(doc.Channel.Items))
	for _, raw := range doc.Channel.Items {
		item := Item{
			Title:      strings.TrimSpace(raw.Title),
			Link:       strings.TrimSpace(raw.Link),
			Categories: cleanStrings(raw.Categories),
			Published:  parsePubDate(raw.PubDate),
		}
		if raw.Enclosure != nil {
			item.Enclosure = Enclosure{
				URL:    strings.TrimSpace(raw.Enclosure.URL),
				Type:   strings.TrimSpace(raw.Enclosure.Type),
				Length: parseLength(raw.Enclosure.Length),
			}
		} else {
			item.Enclosure.Length = -1
		}
		item.ID = firstNonEmpty(strings.TrimSpace(raw.GUID), item.Enclosure.URL, item.Link)
		out = append(out, item)
	}
	return out, nil
}

// --- Atom ---

type atomDoc struct {
	Entries []atomEntry `xml:"entry"`
}

type atomEntry struct {
	Title      string     `xml:"title"`
	Links      []atomLink `xml:"link"`
	ID         string     `xml:"id"`
	Categories []atomCat  `xml:"category"`
	Updated    string     `xml:"updated"`
	Published  string     `xml:"published"`
}

type atomLink struct {
	Href   string `xml:"href,attr"`
	Rel    string `xml:"rel,attr"`
	Type   string `xml:"type,attr"`
	Length string `xml:"length,attr"`
}

type atomCat struct {
	Term string `xml:"term,attr"`
}

func parseAtom(data []byte) ([]Item, error) {
	var doc atomDoc
	if err := xml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse Atom: %w", err)
	}
	out := make([]Item, 0, len(doc.Entries))
	for _, raw := range doc.Entries {
		item := Item{
			Title:     strings.TrimSpace(raw.Title),
			Published: parseAtomDate(firstNonEmpty(raw.Published, raw.Updated)),
		}
		cats := make([]string, 0, len(raw.Categories))
		for _, c := range raw.Categories {
			if strings.TrimSpace(c.Term) != "" {
				cats = append(cats, strings.TrimSpace(c.Term))
			}
		}
		item.Categories = cats
		item.Enclosure.Length = -1
		for _, l := range raw.Links {
			href := strings.TrimSpace(l.Href)
			if href == "" {
				continue
			}
			if l.Rel == "enclosure" && item.Enclosure.URL == "" {
				item.Enclosure = Enclosure{URL: href, Type: strings.TrimSpace(l.Type), Length: parseLength(l.Length)}
			} else if item.Link == "" {
				item.Link = href
			}
		}
		item.ID = firstNonEmpty(strings.TrimSpace(raw.ID), item.Enclosure.URL, item.Link)
		out = append(out, item)
	}
	return out, nil
}

func parseLength(s string) int64 {
	if s == "" {
		return -1
	}
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	// A zero length is how feeds report "unknown"; never treat it as real.
	if err != nil || n <= 0 {
		return -1
	}
	return n
}

func parsePubDate(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC1123Z, time.RFC1123, time.RFC822Z, time.RFC822, time.RFC3339} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

func parseAtomDate(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t
	}
	return time.Time{}
}

func cleanStrings(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
