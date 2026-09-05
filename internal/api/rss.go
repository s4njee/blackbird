package api

import (
	"encoding/json"
	"net/http"
	"time"

	"blackbird/internal/config"
	"blackbird/internal/rss"
)

// secretMask stands in for stored feed credentials in the settings API. A
// submitted mask value keeps the stored secret; an empty value clears it.
const secretMask = "***"

// ---- GET /api/rss, POST /api/rss/add, POST /api/rss/read (PAR-3.3) ----

type rssFeedDTO struct {
	Name         string `json:"name"`
	URL          string `json:"url"`
	Label        string `json:"label"`
	Destination  string `json:"destination"`
	PollInterval int64  `json:"pollIntervalNs"`
	LastFetch    string `json:"lastFetch,omitempty"`
	LastOK       string `json:"lastOk,omitempty"`
	LastError    string `json:"lastError,omitempty"`
	RetryInSecs  int64  `json:"retryInSecs"`
	Items        int    `json:"items"`
	Unread       int    `json:"unread"`
}

type rssItemDTO struct {
	Feed          string   `json:"feed"`
	ID            string   `json:"id"`
	Title         string   `json:"title"`
	Link          string   `json:"link"`
	EnclosureURL  string   `json:"enclosureUrl"`
	EnclosureType string   `json:"enclosureType,omitempty"`
	Length        int64    `json:"length"`
	Categories    []string `json:"categories"`
	Published     string   `json:"published,omitempty"`
	Read          bool     `json:"read"`
	Loaded        bool     `json:"loaded"`
	LoadedHash    string   `json:"loadedHash,omitempty"`
	LoadedBy      string   `json:"loadedBy,omitempty"`
}

type rssEvalDTO struct {
	At      string `json:"at"`
	Feed    string `json:"feed"`
	ItemID  string `json:"itemId"`
	Title   string `json:"title"`
	Outcome string `json:"outcome"`
	Reason  string `json:"reason"`
}

type rssFilterDTO struct {
	Name      string       `json:"name"`
	Feed      string       `json:"feed,omitempty"`
	Evaluated int          `json:"evaluated"`
	Matched   int          `json:"matched"`
	Loaded    int          `json:"loaded"`
	History   []rssEvalDTO `json:"history"`
}

type rssViewDTO struct {
	Feeds   []rssFeedDTO   `json:"feeds"`
	Items   []rssItemDTO   `json:"items"`
	Filters []rssFilterDTO `json:"filters"`
}

func formatRSSTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func (s *Server) rssService() *rss.Service {
	if s.opts.RSS == nil {
		return nil
	}
	return s.opts.RSS
}

func (s *Server) rssViewHandler(w http.ResponseWriter, r *http.Request) {
	svc := s.rssService()
	if svc == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "unavailable", "rss service is not running")
		return
	}
	view := svc.Snapshot()
	out := rssViewDTO{Feeds: []rssFeedDTO{}, Items: []rssItemDTO{}, Filters: []rssFilterDTO{}}
	for _, f := range view.Feeds {
		out.Feeds = append(out.Feeds, rssFeedDTO{
			Name: f.Name, URL: f.URL, Label: f.Label, Destination: f.Destination,
			PollInterval: int64(f.PollInterval), LastFetch: formatRSSTime(f.LastFetch),
			LastOK: formatRSSTime(f.LastOK), LastError: f.LastError,
			RetryInSecs: int64(f.RetryIn / time.Second), Items: f.Items, Unread: f.Unread,
		})
	}
	for _, item := range view.Items {
		out.Items = append(out.Items, rssItemDTO{
			Feed: item.Feed, ID: item.ID, Title: item.Title, Link: item.Link,
			EnclosureURL: item.EnclosureURL, EnclosureType: item.EnclosureType,
			Length: item.Length, Categories: item.Categories,
			Published: formatRSSTime(item.Published), Read: item.Read,
			Loaded: item.Loaded, LoadedHash: item.LoadedHash, LoadedBy: item.LoadedBy,
		})
	}
	for _, f := range view.Filters {
		dto := rssFilterDTO{
			Name: f.Name, Feed: f.Feed, Evaluated: f.Evaluated,
			Matched: f.Matched, Loaded: f.Loaded, History: []rssEvalDTO{},
		}
		for _, e := range f.History {
			dto.History = append(dto.History, rssEvalDTO{
				At: formatRSSTime(e.At), Feed: e.Feed, ItemID: e.ItemID,
				Title: e.Title, Outcome: e.Outcome, Reason: e.Reason,
			})
		}
		out.Filters = append(out.Filters, dto)
	}
	writeJSON(w, http.StatusOK, out)
}

type rssAddRequest struct {
	Feed string `json:"feed"`
	ID   string `json:"id"`
}

func (s *Server) rssAddHandler(w http.ResponseWriter, r *http.Request) {
	svc := s.rssService()
	if svc == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "unavailable", "rss service is not running")
		return
	}
	var req rssAddRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "bad_request", "invalid JSON body: "+err.Error())
		return
	}
	if req.Feed == "" || req.ID == "" {
		writeAPIError(w, http.StatusBadRequest, "bad_request", "feed and id are required")
		return
	}
	hash, err := svc.AddItem(req.Feed, req.ID)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "rss_add_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"hash": hash})
}

type rssReadRequest struct {
	Feed string   `json:"feed,omitempty"`
	IDs  []string `json:"ids,omitempty"`
	All  bool     `json:"all,omitempty"`
}

func (s *Server) rssReadHandler(w http.ResponseWriter, r *http.Request) {
	svc := s.rssService()
	if svc == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "unavailable", "rss service is not running")
		return
	}
	var req rssReadRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "bad_request", "invalid JSON body: "+err.Error())
		return
	}
	marked := svc.MarkRead(req.Feed, req.IDs, req.All || len(req.IDs) == 0)
	writeJSON(w, http.StatusOK, map[string]any{"marked": marked})
}

// ---- Secret redaction for the settings API ----

// redactAutomation returns a copy of the automation section with feed
// credentials replaced by the mask, so secrets never leave the server.
func redactAutomation(a config.Automation) config.Automation {
	out := a
	out.Rss.Feeds = make([]config.RSSFeed, len(a.Rss.Feeds))
	for i, f := range a.Rss.Feeds {
		out.Rss.Feeds[i] = f
		if f.Cookies != "" {
			out.Rss.Feeds[i].Cookies = secretMask
		}
		if len(f.Headers) > 0 {
			masked := make(map[string]string, len(f.Headers))
			for k := range f.Headers {
				masked[k] = secretMask
			}
			out.Rss.Feeds[i].Headers = masked
		}
	}
	return out
}

// restoreRSSSecrets merges submitted automation rules with the stored config:
// mask values keep the stored secret, empty values clear it. Feeds are
// matched by name.
func restoreRSSSecrets(changed *config.Automation, current config.Automation) {
	stored := map[string]config.RSSFeed{}
	for _, f := range current.Rss.Feeds {
		stored[f.Name] = f
	}
	for i := range changed.Rss.Feeds {
		sub := &changed.Rss.Feeds[i]
		prev, ok := stored[sub.Name]
		if !ok {
			// New feed: a mask value with no stored secret means nothing.
			if sub.Cookies == secretMask {
				sub.Cookies = ""
			}
			for k, v := range sub.Headers {
				if v == secretMask {
					delete(sub.Headers, k)
				}
			}
			if len(sub.Headers) == 0 {
				sub.Headers = nil
			}
			continue
		}
		if sub.Cookies == secretMask {
			sub.Cookies = prev.Cookies
		}
		if sub.Headers == nil {
			continue
		}
		merged := map[string]string{}
		for k, v := range sub.Headers {
			if v == secretMask {
				if kept, ok := prev.Headers[k]; ok {
					merged[k] = kept
				}
				continue
			}
			merged[k] = v
		}
		sub.Headers = merged
		if len(sub.Headers) == 0 {
			sub.Headers = nil
		}
	}
}
