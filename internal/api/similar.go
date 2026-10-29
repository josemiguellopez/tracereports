package api

import (
	"log/slog"
	"math"
	"net/http"

	"github.com/josemiguellopez/tracereports/internal/db"
	"github.com/josemiguellopez/tracereports/internal/similar"
)

// Incidentes parecidos (#44): al clasificar un fallo, su firma entra al índice; al abrir otro fallo,
// se buscan los clasificados que más se le parecen en el mismo proyecto.

const (
	similarTopK = 3
	// similarMin: por debajo de esto el parecido no ayuda (comparten poco más que la categoría)
	similarMin = 0.6
)

// indexVerdict puts the signature of a classified test in the index. A failure only logs: the
// verdict is already saved.
func (s *Server) indexVerdict(v *db.Verdict, project string) {
	if s.AI == nil {
		return
	}
	sig, err := s.AI.Signature(v.TestID)
	if err == nil {
		err = s.Store.IndexVerdict(v, project, similar.Terms(sig))
	}
	if err != nil {
		slog.Warn("similar incidents: index verdict", "test_id", v.TestID, "err", err)
	}
}

// SimilarIncident is a classified failure that looks like the one asked about.
type SimilarIncident struct {
	db.SimilarEntry
	Score int `json:"score"` // % de parecido
}

// similarIncidents answers GET /tests/{test_id}/similar: up to 3 classified failures of the same
// project that look like this one (at least 60 %), most similar first.
func (s *Server) similarIncidents(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "test_id")
	if !ok {
		return
	}
	t, run, ok := s.testAndRun(w, id)
	if !ok {
		return
	}
	out := []SimilarIncident{}
	if t.Status != "FAIL" || s.AI == nil {
		writeJSON(w, http.StatusOK, out)
		return
	}
	sig, err := s.AI.Signature(t.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	cands, err := s.Store.SimilarCandidates(run.Project, t.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	docs := make([]similar.Doc, len(cands))
	byID := make(map[int64]db.SimilarEntry, len(cands))
	for i, c := range cands {
		docs[i] = similar.Doc{ID: c.TestID, Terms: c.Terms, CreatedAt: c.CreatedAt}
		byID[c.TestID] = c
	}
	for _, m := range similar.Rank(similar.Terms(sig), docs, similarTopK, similarMin) {
		out = append(out, SimilarIncident{SimilarEntry: byID[m.ID], Score: int(math.Round(m.Score * 100))})
	}
	writeJSON(w, http.StatusOK, out)
}
