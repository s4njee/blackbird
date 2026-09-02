// Package rtorrent is a typed client over the SCGI/XML-RPC transport, so
// handlers never build raw XML-RPC calls. State shapes match the design
// handoff's torrents[] / torrentDetail[hash] models.
package rtorrent

import "time"

// State is the normalized torrent state shown in the UI.
type State string

const (
	StateDownloading State = "downloading"
	StateSeeding     State = "seeding"
	StateStopped     State = "stopped"
	StateQueued      State = "queued"
	StateChecking    State = "checking"
	StateError       State = "error"
)

// AllStates enumerates every normalized state (used for aggregate counts).
var AllStates = []State{StateDownloading, StateSeeding, StateStopped, StateQueued, StateChecking, StateError}

// Torrent is one row of the normalized session model.
type Torrent struct {
	Hash            string    `json:"hash"`
	Name            string    `json:"name"`
	SizeBytes       int64     `json:"sizeBytes"`
	CompletedBytes  int64     `json:"completedBytes"`
	LeftBytes       int64     `json:"leftBytes"`
	DownloadedBytes int64     `json:"downloadedBytes"`
	UploadedBytes   int64     `json:"uploadedBytes"`
	Percent         float64   `json:"percent"`  // derived: completed/size
	Complete        bool      `json:"complete"` // d.complete; independent of the UI state
	IsOpen          bool      `json:"isOpen"`   // d.is_open; used by the Inactive category
	State           State     `json:"state"`
	Message         string    `json:"message"`         // daemon error message when State == error
	CheckingPercent float64   `json:"checkingPercent"` // progress while hash-checking (best effort)
	Seeds           int       `json:"seeds"`           // connected peers with the complete file
	Peers           int       `json:"peers"`           // connected leechers
	DownRate        int64     `json:"downRate"`        // bytes/s
	UpRate          int64     `json:"upRate"`          // bytes/s
	EtaSeconds      float64   `json:"etaSeconds"`      // derived; -1 = not computable (∞ in UI)
	Ratio           float64   `json:"ratio"`
	Label           string    `json:"label"` // d.custom1
	Custom2         string    `json:"custom2"`
	Custom3         string    `json:"custom3"`
	Custom4         string    `json:"custom4"`
	Custom5         string    `json:"custom5"`
	RatioGroup      string    `json:"ratioGroup"` // d.custom2, when used by ratio-group tooling
	Throttle        string    `json:"throttle"`
	TiedToFile      string    `json:"tiedToFile"`
	SkippedBytes    int64     `json:"skippedBytes"`
	PeersAccounted  int       `json:"peersAccounted"`
	ChunksHashed    int64     `json:"chunksHashed"`
	IsMultiFile     bool      `json:"isMultiFile"`
	Directory       string    `json:"directory"`
	Connection      string    `json:"connection"`
	AddedAt         time.Time `json:"addedAt"`
	FinishedAt      time.Time `json:"finishedAt"`
	CreationDate    time.Time `json:"creationDate"`
	TrackerHost     string    `json:"trackerHost"`
	TrackerStatus   string    `json:"trackerStatus"`
	IsPrivate       bool      `json:"isPrivate"`
	BasePath        string    `json:"basePath"`
	Priority        int       `json:"priority"`     // 0 off, 1 low, 2 normal, 3 high
	Superseeding    bool      `json:"superseeding"` // d.connection_seed
	Sequential      bool      `json:"sequential"`   // d.sequential
}

// File is one entry of the f.multicall detail.
type File struct {
	Index           int    `json:"index"`
	Path            string `json:"path"`
	SizeBytes       int64  `json:"sizeBytes"`
	CompletedChunks int64  `json:"completedChunks"`
	SizeChunks      int64  `json:"sizeChunks"`
	Priority        int    `json:"priority"` // 0 skip, 1 normal, 2 high
}

// Percent returns the file completion percentage (derived).
func (f File) Percent() float64 {
	if f.SizeChunks <= 0 {
		return 0
	}
	return clamp100(float64(f.CompletedChunks) / float64(f.SizeChunks) * 100)
}

// Peer is one entry of the p.multicall detail.
type Peer struct {
	ID               string  `json:"id"`
	Address          string  `json:"address"`
	Port             int     `json:"port"`
	Client           string  `json:"client"`
	CompletedPercent float64 `json:"completedPercent"`
	DownRate         int64   `json:"downRate"`
	UpRate           int64   `json:"upRate"`
	Flags            string  `json:"flags"` // composed letters (E encrypted, I incoming, S snubbed)
}

// Tracker is one entry of the t.multicall detail.
type Tracker struct {
	Index        int       `json:"index"`
	URL          string    `json:"url"`
	IsEnabled    bool      `json:"isEnabled"`
	Group        int       `json:"group"`
	Seeds        int       `json:"seeds"`
	Leechers     int       `json:"leechers"`
	NextAnnounce time.Time `json:"nextAnnounceAt"` // resolved from t.next_scrape
	LatestEvent  string    `json:"latestEvent"`
	FailedCount  int       `json:"failedCount"`
	SuccessCount int       `json:"successCount"`
	NewPeers     int       `json:"newPeers"`
}

// Transfer holds per-torrent transfer facts for the detail panel.
type Transfer struct {
	DownloadedBytes int64  `json:"downloadedBytes"`
	UploadedBytes   int64  `json:"uploadedBytes"`
	ChunkSize       int64  `json:"chunkSize"`
	ChunkCount      int64  `json:"chunkCount"`
	ChunksDone      int64  `json:"chunksDone"`
	Directory       string `json:"directory"`
}

// Detail bundles everything the detail panel needs for one torrent.
type Detail struct {
	Hash     string    `json:"hash"`
	Files    []File    `json:"files"`
	Peers    []Peer    `json:"peers"`
	Trackers []Tracker `json:"trackers"`
	Transfer Transfer  `json:"transfer"`
}

// GlobalStats is the global session snapshot for the status bar and cards.
type GlobalStats struct {
	DownRate         int64   `json:"downRate"`
	UpRate           int64   `json:"upRate"`
	SessionUpTotal   int64   `json:"sessionUpTotal"`
	SessionDownTotal int64   `json:"sessionDownTotal"`
	SessionRatio     float64 `json:"sessionRatio"` // derived from session totals
	Version          string  `json:"version"`      // rtorrent
	LibraryVersion   string  `json:"libraryVersion"`
	Port             int     `json:"port"`
	DHTNodes         int     `json:"dhtNodes"`
}
