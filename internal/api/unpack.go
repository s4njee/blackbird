package api

import (
	"net/http"
)

// ---- GET /api/unpack (PAR-3.4) ----

type unpackJobDTO struct {
	Hash      string `json:"hash"`
	Name      string `json:"name"`
	Rule      string `json:"rule"`
	Archive   string `json:"archive"`
	DestDir   string `json:"destDir"`
	Percent   int    `json:"percent"`
	StartedAt string `json:"startedAt"`
}

type unpackStatusDTO struct {
	Available bool           `json:"available"`
	Binary    string         `json:"binary,omitempty"`
	Workers   int            `json:"workers"`
	Queue     int            `json:"queue"`
	Jobs      []unpackJobDTO `json:"jobs"`
}

// unpackStatusHandler reports extractor availability (drives the Settings
// message when 7z is missing), pool depth, and in-flight extractions.
func (s *Server) unpackStatusHandler(w http.ResponseWriter, r *http.Request) {
	svc := s.opts.Unpack
	if svc == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "unavailable", "unpack service is not running")
		return
	}
	status := svc.Status()
	out := unpackStatusDTO{
		Available: status.Available,
		Binary:    status.Binary,
		Workers:   status.Workers,
		Queue:     status.Queue,
		Jobs:      []unpackJobDTO{},
	}
	for _, job := range status.Jobs {
		out.Jobs = append(out.Jobs, unpackJobDTO{
			Hash:      job.Hash,
			Name:      job.Name,
			Rule:      job.Rule,
			Archive:   job.Archive,
			DestDir:   job.DestDir,
			Percent:   job.Percent,
			StartedAt: job.StartedAt.UTC().Format("2006-01-02T15:04:05Z"),
		})
	}
	writeJSON(w, http.StatusOK, out)
}
