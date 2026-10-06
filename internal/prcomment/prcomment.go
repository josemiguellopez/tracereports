// Package prcomment writes the summary of a run as a pull request comment (GitHub) or merge
// request note (GitLab): failures, what changed against the previous run, the incidents with
// their likely cause and the report link. The comment carries a hidden marker, so the next push
// updates it instead of adding another one.
package prcomment

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ---------- datos (lo que responde la API de TraceReports) ----------

// Run is GET /api/v1/runs/{id}.
type Run struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Environment string `json:"environment"`
	Status      string `json:"status"`
	StartedAt   int64  `json:"started_at"`
	EndedAt     *int64 `json:"ended_at"`
	Project     string `json:"project"`
	Branch      string `json:"branch"`
	Commit      string `json:"commit"`
	Incomplete  bool   `json:"incomplete"`
	Total       int    `json:"total"`
	Passed      int    `json:"passed"`
	Failed      int    `json:"failed"`
	Skipped     int    `json:"skipped"`
	Tests       []struct {
		ID           int64  `json:"id"`
		Name         string `json:"name"`
		Status       string `json:"status"`
		ErrorMessage string `json:"error_message"`
		Flaky        bool   `json:"flaky"`
		Attempts     int    `json:"attempts"`
	} `json:"tests"`
	Summary *struct {
		State     string `json:"state"`
		Headline  string `json:"headline"`
		Incidents []struct {
			Title     string   `json:"title"`
			TestNames []string `json:"test_names"`
			Cause     string   `json:"cause"`
			Action    string   `json:"action"`
		} `json:"incidents"`
	} `json:"summary"`
}

// Item is a test in a comparison.
type Item struct {
	Name   string `json:"name"`
	TestID int64  `json:"test_id"`
}

// Comparison is GET /api/v1/runs/{id}/compare.
type Comparison struct {
	BaseRun *struct {
		ID int64 `json:"id"`
	} `json:"base_run"`
	NewFailures []Item `json:"new_failures"`
	Fixed       []Item `json:"fixed"`
}

// ---------- el comentario ----------

var labels = map[string]map[string]string{
	"es": {"passed": "Pasaron", "failed": "Fallaron", "skipped": "Saltados", "duration": "Duración", "allok": "todo en verde",
		"failing": "{f} de {t} fallaron", "incomplete": "ejecución incompleta", "diagnosis": "Diagnóstico", "incidents": "Incidentes",
		"tests": "tests", "cause": "Causa probable", "action": "Revisar", "new": "Fallos nuevos frente a #{b}", "fixed": "Arreglados",
		"flaky": "Flaky (pasan y fallan sin cambios)", "failures": "Fallos", "more": "y {n} más", "report": "Ver el reporte",
		"retry": "pasó tras reintento", "footer": "Comentario de TraceReports: se actualiza en cada push."},
	"en": {"passed": "Passed", "failed": "Failed", "skipped": "Skipped", "duration": "Duration", "allok": "all green",
		"failing": "{f} of {t} failed", "incomplete": "incomplete run", "diagnosis": "Diagnosis", "incidents": "Incidents",
		"tests": "tests", "cause": "Likely cause", "action": "Check", "new": "New failures since #{b}", "fixed": "Fixed",
		"flaky": "Flaky (pass and fail with no change)", "failures": "Failures", "more": "and {n} more", "report": "Open the report",
		"retry": "passed on retry", "footer": "TraceReports comment: updated on every push."},
}

const maxItems = 10

// Marker identifies the comment of one suite (project + run name) in a pull request.
func Marker(r *Run) string {
	return "<!-- tracereports:" + strings.ReplaceAll(r.Project+"/"+r.Name, "--", "-") + " -->"
}

// Markdown renders the comment. link is the report URL ("" for none).
func Markdown(r *Run, cmp *Comparison, lang, link string) string {
	l := labels[lang]
	if l == nil {
		l = labels["en"]
	}
	var b strings.Builder
	b.WriteString(Marker(r) + "\n")
	icon, state := "✅", l["allok"]
	if r.Failed > 0 {
		icon, state = "❌", strings.NewReplacer("{f}", strconv.Itoa(r.Failed), "{t}", strconv.Itoa(r.Total)).Replace(l["failing"])
	}
	if r.Incomplete {
		icon, state = "⚠️", state+" · "+l["incomplete"]
	}
	title := r.Name
	if r.Environment != "" {
		title += " · " + r.Environment
	}
	fmt.Fprintf(&b, "### %s TraceReports · %s — %s\n\n", icon, md(title), state)
	dur := "—"
	if r.EndedAt != nil && *r.EndedAt > r.StartedAt {
		dur = (time.Duration(*r.EndedAt-r.StartedAt) * time.Millisecond).Round(time.Second).String()
	}
	fmt.Fprintf(&b, "| %s | %s | %s | %s |\n|---:|---:|---:|---:|\n| %d | %d | %d | %s |\n\n", l["passed"], l["failed"], l["skipped"], l["duration"],
		r.Passed, r.Failed, r.Skipped, dur)

	if s := r.Summary; s != nil && s.Headline != "" && r.Failed > 0 {
		fmt.Fprintf(&b, "**%s:** %s\n\n", l["diagnosis"], md(s.Headline))
		if len(s.Incidents) > 0 {
			fmt.Fprintf(&b, "**%s**\n", l["incidents"])
			for i, inc := range s.Incidents {
				if i == 3 {
					fmt.Fprintf(&b, "- _%s_\n", strings.ReplaceAll(l["more"], "{n}", strconv.Itoa(len(s.Incidents)-3)))
					break
				}
				fmt.Fprintf(&b, "- **%s** (%d %s)", md(inc.Title), len(inc.TestNames), l["tests"])
				if inc.Cause != "" {
					fmt.Fprintf(&b, " — %s: %s", l["cause"], md(inc.Cause))
				}
				if inc.Action != "" {
					fmt.Fprintf(&b, " · %s: %s", l["action"], md(inc.Action))
				}
				b.WriteString("\n")
			}
			b.WriteString("\n")
		}
	}

	if cmp != nil && cmp.BaseRun != nil {
		names := func(items []Item) []string {
			out := make([]string, len(items))
			for i, it := range items {
				out[i] = it.Name
			}
			return out
		}
		list(&b, strings.ReplaceAll(l["new"], "{b}", strconv.FormatInt(cmp.BaseRun.ID, 10)), names(cmp.NewFailures), l)
		list(&b, l["fixed"], names(cmp.Fixed), l)
	} else if r.Failed > 0 { // sin ejecución anterior con qué comparar: los fallos
		var failed []string
		for _, t := range r.Tests {
			if t.Status == "FAIL" {
				failed = append(failed, t.Name)
			}
		}
		list(&b, l["failures"], failed, l)
	}
	var flaky []string
	for _, t := range r.Tests {
		switch {
		case t.Flaky:
			flaky = append(flaky, t.Name)
		case t.Status == "PASS" && t.Attempts > 1:
			flaky = append(flaky, t.Name+" ("+l["retry"]+")")
		}
	}
	list(&b, l["flaky"], flaky, l)
	if link != "" {
		fmt.Fprintf(&b, "[%s →](%s)\n\n", l["report"], link)
	}
	fmt.Fprintf(&b, "<sub>%s</sub>\n", l["footer"])
	return b.String()
}

func list(b *strings.Builder, title string, items []string, l map[string]string) {
	if len(items) == 0 {
		return
	}
	fmt.Fprintf(b, "**%s**\n", title)
	for i, it := range items {
		if i == maxItems {
			fmt.Fprintf(b, "- _%s_\n", strings.ReplaceAll(l["more"], "{n}", strconv.Itoa(len(items)-maxItems)))
			break
		}
		fmt.Fprintf(b, "- `%s`\n", strings.ReplaceAll(it, "`", "'"))
	}
	b.WriteString("\n")
}

// md neutralizes what would break the comment layout or ping people (@user, #123 references).
func md(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	return strings.NewReplacer("@", "@​", "<", "&lt;", ">", "&gt;", "|", "\\|").Replace(s)
}

// ---------- plataformas ----------

// Platform posts (or updates) the comment of a pull or merge request.
type Platform interface {
	Name() string
	// Upsert updates the comment that carries marker, or creates it. Returns its URL.
	Upsert(ctx context.Context, marker, body string) (string, error)
}

// Detect finds the pull request of the current CI job: GitHub Actions (GITHUB_TOKEN with
// pull-requests: write) or GitLab CI (a token with api scope in GITLAB_TOKEN, since CI_JOB_TOKEN
// cannot write notes). getenv is os.Getenv in production. nil, nil when not in a PR.
func Detect(getenv func(string) string, client *http.Client) (Platform, error) {
	if getenv("GITHUB_ACTIONS") == "true" {
		n := githubPR(getenv)
		if n == 0 {
			return nil, nil
		}
		token := first(getenv("TRACEREPORTS_GITHUB_TOKEN"), getenv("GITHUB_TOKEN"))
		if token == "" {
			return nil, errors.New("GitHub: set GITHUB_TOKEN (env: GITHUB_TOKEN: ${{ github.token }}, with permissions: pull-requests: write)")
		}
		return &GitHub{API: first(getenv("GITHUB_API_URL"), "https://api.github.com"), Repo: getenv("GITHUB_REPOSITORY"),
			Number: n, Token: token, HTTP: client}, nil
	}
	if getenv("GITLAB_CI") == "true" {
		iid, _ := strconv.Atoi(getenv("CI_MERGE_REQUEST_IID"))
		if iid == 0 {
			return nil, nil
		}
		token := first(getenv("TRACEREPORTS_GITLAB_TOKEN"), getenv("GITLAB_TOKEN"))
		if token == "" {
			return nil, errors.New("GitLab: set GITLAB_TOKEN (a project or personal access token with api scope)")
		}
		return &GitLab{API: first(getenv("CI_API_V4_URL"), "https://gitlab.com/api/v4"), Project: getenv("CI_PROJECT_ID"),
			MR: iid, Token: token, WebURL: getenv("CI_MERGE_REQUEST_PROJECT_URL"), HTTP: client}, nil
	}
	return nil, nil
}

var pullRef = regexp.MustCompile(`^refs/pull/(\d+)/`)

// githubPR reads the pull request number from the event payload, or from GITHUB_REF.
func githubPR(getenv func(string) string) int {
	if p := getenv("GITHUB_EVENT_PATH"); p != "" {
		if raw, err := os.ReadFile(p); err == nil {
			var ev struct {
				PullRequest struct {
					Number int `json:"number"`
				} `json:"pull_request"`
				Number int `json:"number"`
			}
			if json.Unmarshal(raw, &ev) == nil && ev.PullRequest.Number > 0 {
				return ev.PullRequest.Number
			}
		}
	}
	if m := pullRef.FindStringSubmatch(getenv("GITHUB_REF")); m != nil {
		n, _ := strconv.Atoi(m[1])
		return n
	}
	return 0
}

func first(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// GitHub comments on a pull request (issue comments API).
type GitHub struct {
	API, Repo, Token string
	Number           int
	HTTP             *http.Client
}

func (g *GitHub) Name() string { return fmt.Sprintf("GitHub %s#%d", g.Repo, g.Number) }

func (g *GitHub) Upsert(ctx context.Context, marker, body string) (string, error) {
	base := strings.TrimRight(g.API, "/") + "/repos/" + g.Repo + "/issues"
	h := map[string]string{"Authorization": "Bearer " + g.Token, "Accept": "application/vnd.github+json", "X-GitHub-Api-Version": "2022-11-28"}
	var out struct {
		ID      int64  `json:"id"`
		HTMLURL string `json:"html_url"`
	}
	for page := 1; page <= 10; page++ { // hasta 1000 comentarios
		var comments []struct {
			ID   int64  `json:"id"`
			Body string `json:"body"`
		}
		if err := send(ctx, g.HTTP, http.MethodGet, fmt.Sprintf("%s/%d/comments?per_page=100&page=%d", base, g.Number, page), nil, h, &comments); err != nil {
			return "", err
		}
		for _, c := range comments {
			if strings.Contains(c.Body, marker) {
				err := send(ctx, g.HTTP, http.MethodPatch, fmt.Sprintf("%s/comments/%d", base, c.ID), map[string]string{"body": body}, h, &out)
				return out.HTMLURL, err
			}
		}
		if len(comments) < 100 {
			break
		}
	}
	err := send(ctx, g.HTTP, http.MethodPost, fmt.Sprintf("%s/%d/comments", base, g.Number), map[string]string{"body": body}, h, &out)
	return out.HTMLURL, err
}

// GitLab writes a note on a merge request.
type GitLab struct {
	API, Project, Token string
	MR                  int
	WebURL              string // CI_MERGE_REQUEST_PROJECT_URL, para el link de la nota
	HTTP                *http.Client
}

func (g *GitLab) Name() string { return fmt.Sprintf("GitLab !%d", g.MR) }

func (g *GitLab) Upsert(ctx context.Context, marker, body string) (string, error) {
	base := fmt.Sprintf("%s/projects/%s/merge_requests/%d/notes", strings.TrimRight(g.API, "/"), url.PathEscape(g.Project), g.MR)
	h := map[string]string{"PRIVATE-TOKEN": g.Token}
	var out struct {
		ID int64 `json:"id"`
	}
	for page := 1; page <= 10; page++ {
		var notes []struct {
			ID   int64  `json:"id"`
			Body string `json:"body"`
		}
		if err := send(ctx, g.HTTP, http.MethodGet, fmt.Sprintf("%s?per_page=100&page=%d&sort=asc", base, page), nil, h, &notes); err != nil {
			return "", err
		}
		for _, n := range notes {
			if strings.Contains(n.Body, marker) {
				err := send(ctx, g.HTTP, http.MethodPut, fmt.Sprintf("%s/%d", base, n.ID), map[string]string{"body": body}, h, &out)
				return fmt.Sprintf("%s#note_%d", g.MRURL(), n.ID), err
			}
		}
		if len(notes) < 100 {
			break
		}
	}
	err := send(ctx, g.HTTP, http.MethodPost, base, map[string]string{"body": body}, h, &out)
	return fmt.Sprintf("%s#note_%d", g.MRURL(), out.ID), err
}

// MRURL is the web URL of the merge request (when the project URL is known).
func (g *GitLab) MRURL() string {
	if g.WebURL != "" {
		return fmt.Sprintf("%s/-/merge_requests/%d", strings.TrimRight(g.WebURL, "/"), g.MR)
	}
	return fmt.Sprintf("merge request !%d", g.MR)
}

func send(ctx context.Context, client *http.Client, method, u string, body any, headers map[string]string, out any) error {
	var rd io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("User-Agent", "tracereports")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode >= 300 {
		return fmt.Errorf("%s %s answered HTTP %d: %s", method, req.URL.Host, resp.StatusCode, strings.Join(strings.Fields(string(raw)), " "))
	}
	if out != nil && len(raw) > 0 {
		return json.Unmarshal(raw, out)
	}
	return nil
}
