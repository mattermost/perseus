package server

import (
	"encoding/json"
	"net/http"

	"github.com/mattermost/logr/v2"
)

type healthResponse struct {
	CommitHash   string `json:"commit_hash"`
	BuildVersion string `json:"build_version"`
	BuildDate    string `json:"build_date"`
	GoVersion    string `json:"go_version"`
}

func (s *Server) healthHandler(w http.ResponseWriter, r *http.Request) {
	err := json.NewEncoder(w).Encode(healthResponse{
		CommitHash:   CommitHash,
		BuildVersion: BuildVersion,
		BuildDate:    BuildDate,
		GoVersion:    GoVersion,
	})
	if err != nil {
		s.logger.Error("Failed to write health response", logr.Err(err))
		http.Error(w, "failed to write health response", http.StatusInternalServerError)
		return
	}
}
