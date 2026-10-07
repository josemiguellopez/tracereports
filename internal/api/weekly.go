package api

import (
	"context"
	"net/http"
	"time"

	"github.com/josemiguellopez/tracereports/internal/db"
	"github.com/josemiguellopez/tracereports/internal/notify"
)

// WeeklySummary builds the summary of the last 7 days in the team's language (Settings, or lang).
func (s *Server) WeeklySummary(lang string) (*notify.Escalation, error) {
	if lang == "" {
		saved, err := s.Store.Settings()
		if err != nil {
			return nil, err
		}
		lang = saved[setLanguage]
	}
	m, err := s.Store.Metrics(db.MetricsQuery{Days: 7})
	if err != nil {
		return nil, err
	}
	public := ""
	if s.Notify != nil {
		public = s.Notify.PublicURL()
	}
	return notify.Weekly(m, lang, public), nil
}

// weeklySummary previews the weekly summary ({send: false}) or posts it now to Teams/Slack
// ({send: true}); it is also sent by itself with TRACEREPORTS_WEEKLY_SUMMARY.
func (s *Server) weeklySummary(w http.ResponseWriter, r *http.Request) {
	if !s.guardUIWrite(w, r) {
		return
	}
	var in struct {
		Send bool   `json:"send"`
		Lang string `json:"lang"`
	}
	if !decode(w, r, &in) {
		return
	}
	e, err := s.WeeklySummary(in.Lang)
	if err != nil {
		serverError(w, err)
		return
	}
	out := map[string]any{"summary": e, "sent": []string{}}
	if in.Send {
		if s.Notify == nil {
			writeError(w, http.StatusBadGateway, "no hay canal configurado (TEAMS_WEBHOOK_URL o SLACK_WEBHOOK_URL)")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		sent, err := s.Notify.SendWeekly(ctx, e)
		if err != nil {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
		out["sent"] = sent
	}
	writeJSON(w, http.StatusOK, out)
}
