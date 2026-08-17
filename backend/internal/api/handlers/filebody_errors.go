package handlers

import (
	"errors"
	"net/http"

	"github.com/brf-tech/filex/backend/internal/filebody"
)

// stagingGoneStatus is the answer when a node says its bytes are staged but
// the staging area cannot produce them.
//
// 503, not 404: the file exists, it is listed, and answering "not found" would
// send a user hunting for a file they can see. Not 500 either — this is a known
// state with a known cause (a failed transfer whose staging was swept, or a
// staging directory removed by hand), and the message says so. What it must
// never be is a 200 with a short body.
const stagingGoneStatus = http.StatusServiceUnavailable

// writeStagingGone answers a read that could not be backed by bytes. It
// separates the staging-gone case (a real message, 503) from every other
// backend failure (500), so the two are distinguishable in a log and on screen.
func writeStagingGone(w http.ResponseWriter, err error) {
	if errors.Is(err, filebody.ErrStagingGone) {
		writeJSON(w, stagingGoneStatus, map[string]string{
			"error": err.Error(),
			"code":  "STAGING_GONE",
		})
		return
	}
	writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
}

// stagingGoneText is writeStagingGone for the endpoints that answer in plain
// text (the public media routes), which must not start emitting JSON.
func stagingGoneText(w http.ResponseWriter, err error) {
	if errors.Is(err, filebody.ErrStagingGone) {
		http.Error(w, err.Error(), stagingGoneStatus)
		return
	}
	http.Error(w, "read error", http.StatusInternalServerError)
}
