package api

import (
	"net/http"
)

// BuildInfo carries the Blackbird binary's stamped identity. The values are
// set with -ldflags "-X main.version=… -X main.commit=… -X main.buildDate=…"
// (see Dockerfile); the zero values mean a local `go run` build.
type BuildInfo struct {
	Version   string
	Commit    string
	BuildDate string
}

// VersionResponse is the GET /api/version (and /api/v1/version) payload
// (POL-8.7): the Blackbird build plus the live rTorrent/libtorrent versions
// from the poller snapshot. Daemon fields are empty while disconnected so
// the About panel can degrade to dashes instead of failing the whole
// request. API/WS report the contract versions this server speaks (POL-8.8):
// REST v1 and WebSocket v1 (legacy whole-row) through v2 (field patches).
type VersionResponse struct {
	Blackbird struct {
		Version   string `json:"version"`
		Commit    string `json:"commit"`
		BuildDate string `json:"buildDate"`
	} `json:"blackbird"`
	RTorrent struct {
		Version string `json:"version"`
		Library string `json:"library"`
	} `json:"rtorrent"`
	API struct {
		Version string `json:"version"`
	} `json:"api"`
	WS struct {
		Min     int `json:"min"`
		Current int `json:"current"`
	} `json:"ws"`
	Connection string `json:"connection"`
	Torrents   int    `json:"torrents"`
}

func (s *Server) versionHandler(w http.ResponseWriter, r *http.Request) {
	var out VersionResponse
	out.Blackbird.Version = s.opts.Build.Version
	out.Blackbird.Commit = s.opts.Build.Commit
	out.Blackbird.BuildDate = s.opts.Build.BuildDate
	out.API.Version = "v1"
	out.WS.Min = wsVersionMin
	out.WS.Current = wsVersion
	if out.Blackbird.Version == "" {
		out.Blackbird.Version = "dev"
	}
	if out.Blackbird.Commit == "" {
		out.Blackbird.Commit = "none"
	}
	if out.Blackbird.BuildDate == "" {
		out.Blackbird.BuildDate = "unknown"
	}
	if s.opts.Poller != nil {
		snap := s.opts.Poller.Snapshot()
		out.Connection = string(snap.Status)
		out.Torrents = len(snap.Torrents)
		out.RTorrent.Version = snap.Global.Version
		out.RTorrent.Library = snap.Global.LibraryVersion
	}
	writeJSON(w, http.StatusOK, out)
}
