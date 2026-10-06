package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/josemiguellopez/tracereports/internal/ai"
	"github.com/josemiguellopez/tracereports/internal/notify"
)

// Acciones que dispara una persona desde la interfaz (re-analizar con IA, escalar, enviar a un
// canal). Gastan cuota de IA o publican en Teams/Slack, así que siguen la misma regla que los
// Ajustes: token, login de la UI o, sin login, solo el mismo equipo del servidor.

// uiAccess is settingsAccess without the TRACEREPORTS_SETTINGS_LOCKED switch (it only locks settings).
func (s *Server) uiAccess(r *http.Request) (bool, string) { return s.adminAccess(r) }

func (s *Server) guardUIWrite(w http.ResponseWriter, r *http.Request) bool {
	if ok, reason := s.uiAccess(r); !ok {
		writeError(w, http.StatusForbidden, "this action is not allowed from here ("+reason+")")
		return false
	}
	return jsonSameOrigin(w, r)
}

// ---------- análisis con IA a pedido ----------

func (s *Server) reanalyzeTest(w http.ResponseWriter, r *http.Request) {
	if !s.guardUIWrite(w, r) {
		return
	}
	id, ok := pathID(w, r, "test_id")
	if !ok {
		return
	}
	t, err := s.Store.GetTest(id)
	if respondErr(w, err, "test") {
		return
	}
	if !s.AI.Enabled() {
		writeError(w, http.StatusConflict, "AI is not configured")
		return
	}
	if t.Status != "FAIL" {
		writeError(w, http.StatusBadRequest, "only failed tests are analyzed")
		return
	}
	if !s.AI.ReanalyzeAsync(id) {
		writeError(w, http.StatusConflict, "this test is already being analyzed")
		return
	}
	s.publish("triage", t.RunID, id, map[string]string{"state": "PENDING"})
	writeJSON(w, http.StatusAccepted, map[string]any{"queued": 1})
}

// reanalyzeRun re-runs the per-test analyses that are missing or failed (all of them with
// {"all": true}) and then the run diagnosis.
func (s *Server) reanalyzeRun(w http.ResponseWriter, r *http.Request) {
	if !s.guardUIWrite(w, r) {
		return
	}
	id, ok := pathID(w, r, "run_id")
	if !ok {
		return
	}
	var in struct {
		All bool `json:"all"`
	}
	_ = json.NewDecoder(r.Body).Decode(&in)
	run, err := s.Store.GetRunDetail(id)
	if respondErr(w, err, "run") {
		return
	}
	if !s.AI.Enabled() {
		writeError(w, http.StatusConflict, "AI is not configured")
		return
	}
	queued := 0
	for _, t := range run.Tests {
		if t.Status == "FAIL" && (in.All || t.Triage == nil || t.Triage.State == "ERROR" || t.Triage.State == "SKIPPED") &&
			s.AI.ReanalyzeAsync(t.ID) {
			queued++
		}
	}
	if run.Status != "RUNNING" {
		s.AI.AnalyzeRunAsync(id, nil)
	}
	s.publish("summary", id, 0, map[string]string{"state": "PENDING"})
	writeJSON(w, http.StatusAccepted, map[string]any{"queued": queued})
}

func (s *Server) runRecurrence(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "run_id")
	if !ok {
		return
	}
	rec, err := s.Store.IncidentRecurrence(id, 10)
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

// ---------- escalamiento ----------

type escalationRef struct {
	RunID    int64  `json:"run_id"`
	TestID   int64  `json:"test_id"`
	Audience string `json:"audience"`
	Lang     string `json:"lang"`
}

func (e *escalationRef) valid() error {
	if e.RunID <= 0 {
		return errors.New("run_id is required")
	}
	ok := false
	for _, a := range ai.Audiences {
		ok = ok || a == e.Audience
	}
	if !ok {
		return errors.New("audience must be business, qa or dev")
	}
	if !languages[e.Lang] {
		e.Lang = "es"
	}
	return nil
}

func (s *Server) cachedEscalation(ref escalationRef) (*ai.Escalation, error) {
	raw, err := s.Store.GetEscalation(ref.RunID, ref.TestID, ref.Audience, ref.Lang)
	if err != nil || raw == "" {
		return nil, err
	}
	var e ai.Escalation
	if err := json.Unmarshal([]byte(raw), &e); err != nil {
		return nil, err
	}
	return &e, nil
}

// getEscalation returns a cached escalation (204 when it was never generated).
func (s *Server) getEscalation(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "run_id")
	if !ok {
		return
	}
	q := r.URL.Query()
	testID, _ := strconv.ParseInt(q.Get("test"), 10, 64)
	ref := escalationRef{RunID: id, TestID: testID, Audience: q.Get("audience"), Lang: q.Get("lang")}
	if err := ref.valid(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	e, err := s.cachedEscalation(ref)
	if err != nil {
		serverError(w, err)
		return
	}
	if e == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	writeJSON(w, http.StatusOK, e)
}

// escalate generates (or returns the cached) summary of a run or test for an audience.
func (s *Server) escalate(w http.ResponseWriter, r *http.Request) {
	if !s.guardUIWrite(w, r) {
		return
	}
	var in struct {
		escalationRef
		Regenerate bool `json:"regenerate"`
		NoAI       bool `json:"no_ai"` // plantilla aunque haya IA configurada
	}
	if !decode(w, r, &in) {
		return
	}
	if err := in.valid(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !in.Regenerate && !in.NoAI {
		if e, err := s.cachedEscalation(in.escalationRef); err != nil {
			serverError(w, err)
			return
		} else if e != nil {
			writeJSON(w, http.StatusOK, e)
			return
		}
	}
	facts, err := ai.BuildFacts(s.Store, s.ScreenshotsDir, in.RunID, in.TestID)
	if respondErr(w, err, "run or test") {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()
	var e *ai.Escalation
	if in.NoAI {
		e = ai.EscalateTemplate(facts, in.RunID, in.TestID, in.Audience, in.Lang)
	} else {
		e = s.AI.Escalate(ctx, facts, in.RunID, in.TestID, in.Audience, in.Lang)
	}
	s.withOwner(e) // el dueño de las reglas manda sobre el que sugiere la IA
	if raw, err := json.Marshal(e); err == nil && e.Source == "ai" { // la plantilla es gratis: no se guarda
		if err := s.Store.SaveEscalation(in.RunID, in.TestID, in.Audience, in.Lang, string(raw)); err != nil {
			slog.Warn("escalation: cache", "err", err)
		}
	}
	writeJSON(w, http.StatusOK, e)
}

// escalationFor returns the summary to share: the cached AI one, or a new one (the template is
// immediate; with AI configured and noAI false, the AI writes it).
func (s *Server) escalationFor(ctx context.Context, ref escalationRef, noAI bool) (*ai.Escalation, error) {
	if !noAI {
		if e, err := s.cachedEscalation(ref); err != nil || e != nil {
			return e, err
		}
	}
	facts, err := ai.BuildFacts(s.Store, s.ScreenshotsDir, ref.RunID, ref.TestID)
	if err != nil {
		return nil, err
	}
	var e *ai.Escalation
	if noAI {
		e = ai.EscalateTemplate(facts, ref.RunID, ref.TestID, ref.Audience, ref.Lang)
	} else {
		e = s.AI.Escalate(ctx, facts, ref.RunID, ref.TestID, ref.Audience, ref.Lang)
	}
	s.withOwner(e)
	return e, nil
}

var escalationLabels = map[string]map[string]string{
	"es": {"critical": "Severidad crítica", "high": "Severidad alta", "medium": "Severidad media", "low": "Severidad baja",
		"what": "Qué pasó", "impact": "Impacto", "cause": "Causa probable", "evidence": "Evidencia", "next": "Próximos pasos",
		"owner": "Responsable sugerido", "link": "Ver reporte"},
	"en": {"critical": "Critical severity", "high": "High severity", "medium": "Medium severity", "low": "Low severity",
		"what": "What happened", "impact": "Impact", "cause": "Likely cause", "evidence": "Evidence", "next": "Next steps",
		"owner": "Suggested owner", "link": "View report"},
}

// sendEscalation posts an already generated escalation to Teams or Slack.
func (s *Server) sendEscalation(w http.ResponseWriter, r *http.Request) {
	if !s.guardUIWrite(w, r) {
		return
	}
	var in struct {
		escalationRef
		Channel string `json:"channel"`
		NoAI    bool   `json:"no_ai"`
	}
	if !decode(w, r, &in) {
		return
	}
	if err := in.valid(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	e, err := s.escalationFor(r.Context(), in.escalationRef, in.NoAI)
	if respondErr(w, err, "run or test") {
		return
	}
	l := escalationLabels[in.Lang]
	msg := &notify.Escalation{Title: e.Title, Severity: l[e.Severity], Critical: e.Severity == "critical" || e.Severity == "high",
		Headline: e.Headline, LinkLabel: l["link"],
		Sections: [][2]string{{l["what"], e.WhatHappened}, {l["impact"], e.Impact}, {l["cause"], e.RootCause}, {l["owner"], e.Owner}},
		Lists:    [][2]any{{l["evidence"], e.Evidence}, {l["next"], e.NextSteps}}}
	if base := s.Notify.PublicURL(); base != "" {
		msg.URL = base + "/" + e.Facts.ReportPath
		if e.Facts.Screenshot != "" && strings.HasPrefix(e.Facts.Screenshot, "/") {
			msg.ImageURL = base + e.Facts.Screenshot
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	if err := s.Notify.SendEscalation(ctx, in.Channel, msg); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sent": in.Channel, "with_link": msg.URL != "", "with_image": msg.ImageURL != ""})
}
