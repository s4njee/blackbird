package rss

import (
	"context"
	"fmt"
	"sort"
	"time"
)

// FeedView is one feed's status for the RSS view.
type FeedView struct {
	Name         string
	URL          string
	Label        string
	Destination  string
	PollInterval time.Duration
	LastFetch    time.Time
	LastOK       time.Time
	LastError    string
	RetryIn      time.Duration
	Items        int
	Unread       int
}

// ItemView is one stored item for the RSS view.
type ItemView struct {
	Feed          string
	ID            string
	Title         string
	Link          string
	EnclosureURL  string
	EnclosureType string
	Length        int64
	Categories    []string
	Published     time.Time
	Read          bool
	Loaded        bool
	LoadedHash    string
	LoadedBy      string
}

// FilterView is one filter's counters and match history for the RSS view.
type FilterView struct {
	Name      string
	Feed      string
	Evaluated int
	Matched   int
	Loaded    int
	History   []FilterEval
}

// View is the full RSS state served by GET /api/rss.
type View struct {
	Feeds   []FeedView
	Items   []ItemView
	Filters []FilterView
}

// Snapshot returns a deep copy of the current state for the API.
func (s *Service) Snapshot() View {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.opts.Now()
	view := View{
		Feeds:   []FeedView{},
		Items:   []ItemView{},
		Filters: []FilterView{},
	}
	names := make([]string, 0, len(s.feeds))
	for name := range s.feeds {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		st := s.feeds[name]
		fv := FeedView{
			Name:         name,
			URL:          st.cfg.URL,
			Label:        st.cfg.Label,
			Destination:  st.cfg.Destination,
			PollInterval: st.cfg.EffectivePollInterval(),
			LastFetch:    st.lastFetch,
			LastOK:       st.lastOK,
			LastError:    st.lastError,
			Items:        len(st.items),
		}
		if now.Before(st.nextRetry) {
			fv.RetryIn = st.nextRetry.Sub(now).Round(time.Second)
		}
		for _, item := range st.items {
			if !item.Read && !item.Loaded {
				fv.Unread++
			}
			view.Items = append(view.Items, ItemView{
				Feed:          name,
				ID:            item.ID,
				Title:         item.Title,
				Link:          item.Link,
				EnclosureURL:  item.Enclosure.URL,
				EnclosureType: item.Enclosure.Type,
				Length:        item.Enclosure.Length,
				Categories:    append([]string(nil), item.Categories...),
				Published:     item.Published,
				Read:          item.Read,
				Loaded:        item.Loaded,
				LoadedHash:    item.LoadedHash,
				LoadedBy:      item.LoadedBy,
			})
		}
		view.Feeds = append(view.Feeds, fv)
	}
	filterNames := make([]string, 0, len(s.filters))
	for name := range s.filters {
		filterNames = append(filterNames, name)
	}
	sort.Strings(filterNames)
	feedOf := map[string]string{}
	if s.opts.Filters != nil {
		for _, f := range s.opts.Filters() {
			feedOf[f.Name] = f.Feed
		}
	}
	for _, name := range filterNames {
		fs := s.filters[name]
		view.Filters = append(view.Filters, FilterView{
			Name:      name,
			Feed:      feedOf[name],
			Evaluated: fs.evaluated,
			Matched:   fs.matched,
			Loaded:    fs.loaded,
			History:   append([]FilterEval(nil), fs.history...),
		})
	}
	return view
}

// AddItem manually loads one stored item with the feed defaults.
func (s *Service) AddItem(feedName, itemID string) (string, error) {
	s.mu.Lock()
	st := s.feeds[feedName]
	if st == nil {
		s.mu.Unlock()
		return "", fmt.Errorf("unknown feed %q", feedName)
	}
	stored := st.byID[itemID]
	cfg := st.cfg
	s.mu.Unlock()
	if stored == nil {
		return "", fmt.Errorf("unknown item %q in feed %q", itemID, feedName)
	}
	s.mu.Lock()
	loaded := stored.Loaded
	s.mu.Unlock()
	if loaded {
		return "", fmt.Errorf("item %q is already loaded", stored.Title)
	}
	session := map[string]bool{}
	if s.opts.Snapshot != nil {
		for _, t := range s.opts.Snapshot() {
			session[t.Hash] = true
		}
	}
	outcome, reason, hash := s.loadItem(cfg, stored, "", "", true, session)
	if outcome != "loaded" {
		return "", fmt.Errorf("%s: %s", outcome, reason)
	}
	s.mu.Lock()
	stored.LoadedBy = "manual"
	s.mu.Unlock()
	return hash, nil
}

// MarkRead marks items read: all items of one feed, one feed entirely, or
// everything when feedName is empty. Returns the number marked.
func (s *Service) MarkRead(feedName string, ids []string, all bool) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	marked := 0
	only := map[string]bool{}
	for _, id := range ids {
		only[id] = true
	}
	for name, st := range s.feeds {
		if feedName != "" && name != feedName {
			continue
		}
		for _, item := range st.items {
			if !all && len(only) > 0 && !only[item.ID] {
				continue
			}
			if !item.Read {
				item.Read = true
				marked++
			}
		}
	}
	return marked
}

// PollNow triggers an immediate poll of every idle feed (tests and the
// manual refresh path). It does not wait for completion.
func (s *Service) PollNow(ctx context.Context) {
	s.pollDue(ctx)
}
