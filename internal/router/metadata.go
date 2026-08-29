package router

import (
	"encoding/json"
	"net/http"
)

const (
	// MetadataPath is the reserved path under a Neo HTTP prefix that exposes
	// procedure metadata as JSON. With the default prefix, the endpoint is
	// /neo/_meta.
	MetadataPath = "_meta"

	// BatchPath is the reserved path under a Neo HTTP prefix for JSON batch
	// calls. With the default prefix, the endpoint is /neo/_batch.
	BatchPath = "_batch"
)

func serveProcedureMetadata(w http.ResponseWriter, r *http.Request, metadata []ProcedureMeta) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD, OPTIONS")
		writeProcedureError(w, NewError(CodeMethodNotAllowed, "metadata requires GET"))
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}

	_ = json.NewEncoder(w).Encode(metadata)
}
