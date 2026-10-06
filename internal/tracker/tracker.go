// Package tracker creates tickets in an issue tracker (GitHub Issues, Jira, Azure DevOps) from a
// failure summary. Each provider renders the same Issue in its own format (Markdown, Jira wiki
// markup, HTML) and attaches the screenshot when its API allows it.
//
// Configuration comes from the environment; tokens never leave this package (the UI only learns
// which providers exist):
//
//	TRACEREPORTS_GITHUB_REPO  owner/repo          TRACEREPORTS_GITHUB_TOKEN  token with issues:write
//	TRACEREPORTS_GITHUB_API   API URL (GitHub Enterprise; default https://api.github.com)
//	TRACEREPORTS_GITHUB_LABELS labels (default bug)
//
//	TRACEREPORTS_JIRA_URL     https://acme.atlassian.net   TRACEREPORTS_JIRA_PROJECT  project key (SHOP)
//	TRACEREPORTS_JIRA_TOKEN   API token (Cloud, with TRACEREPORTS_JIRA_EMAIL) or personal access token (Server/DC)
//	TRACEREPORTS_JIRA_ISSUE_TYPE (default Bug)   TRACEREPORTS_JIRA_LABELS (default tracereports)
//
//	TRACEREPORTS_AZURE_URL    https://dev.azure.com/acme   TRACEREPORTS_AZURE_PROJECT  project
//	TRACEREPORTS_AZURE_TOKEN  personal access token (Work Items: read & write)
//	TRACEREPORTS_AZURE_TYPE   work item type (default Bug)   TRACEREPORTS_AZURE_TAGS (default tracereports)
package tracker

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/josemiguellopez/tracereports/internal/env"
)

// Issue is what a ticket says, independent of the tracker.
type Issue struct {
	Title    string
	Headline string
	Sections [][2]string // título, texto
	Lists    []List
	Code     [][2]string // título, bloque (error, traza)
	Link     string      // al reporte (vacío sin PUBLIC_URL)
	LinkText string
	Footer   string
	Image    []byte // captura del fallo (PNG/JPEG), opcional
}

// List is a titled bullet list.
type List struct {
	Title string
	Items []string
}

// Ticket is a created issue.
type Ticket struct {
	Provider string `json:"provider"`
	Key      string `json:"key"`
	URL      string `json:"url"`
}

// Provider creates tickets in one tracker.
type Provider interface {
	ID() string   // github | jira | azure
	Name() string // GitHub | Jira | Azure DevOps
	Create(ctx context.Context, is *Issue) (*Ticket, error)
}

// FromEnv returns the configured providers (none when nothing is set).
func FromEnv() []Provider {
	client := &http.Client{Timeout: 30 * time.Second}
	var out []Provider
	if repo, token := env.Get("GITHUB_REPO"), env.Get("GITHUB_TOKEN"); repo != "" && token != "" {
		out = append(out, &GitHub{API: or(env.Get("GITHUB_API"), "https://api.github.com"), Repo: repo, Token: token,
			Labels: list(or(env.Get("GITHUB_LABELS"), "bug")), HTTP: client})
	}
	if u, p, t := env.Get("JIRA_URL"), env.Get("JIRA_PROJECT"), env.Get("JIRA_TOKEN"); u != "" && p != "" && t != "" {
		out = append(out, &Jira{URL: u, Project: p, Email: env.Get("JIRA_EMAIL"), Token: t,
			IssueType: or(env.Get("JIRA_ISSUE_TYPE"), "Bug"), Labels: list(or(env.Get("JIRA_LABELS"), "tracereports")), HTTP: client})
	}
	if u, p, t := env.Get("AZURE_URL"), env.Get("AZURE_PROJECT"), env.Get("AZURE_TOKEN"); u != "" && p != "" && t != "" {
		out = append(out, &Azure{URL: u, Project: p, Token: t, Type: or(env.Get("AZURE_TYPE"), "Bug"),
			Tags: list(or(env.Get("AZURE_TAGS"), "tracereports")), HTTP: client})
	}
	return out
}

func or(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return strings.TrimSpace(v)
}

func list(v string) []string {
	var out []string
	for _, s := range strings.Split(v, ",") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// ---------- GitHub Issues ----------

// GitHub creates issues in a repository (github.com or GitHub Enterprise).
type GitHub struct {
	API, Repo, Token string
	Labels           []string
	HTTP             *http.Client
}

func (g *GitHub) ID() string   { return "github" }
func (g *GitHub) Name() string { return "GitHub" }

func (g *GitHub) Create(ctx context.Context, is *Issue) (*Ticket, error) {
	body := map[string]any{"title": clip(is.Title, 250), "body": Markdown(is)}
	if len(g.Labels) > 0 {
		body["labels"] = g.Labels
	}
	var out struct {
		Number  int    `json:"number"`
		HTMLURL string `json:"html_url"`
	}
	err := call(ctx, g.HTTP, http.MethodPost, strings.TrimRight(g.API, "/")+"/repos/"+g.Repo+"/issues", body,
		map[string]string{"Authorization": "Bearer " + g.Token, "Accept": "application/vnd.github+json", "X-GitHub-Api-Version": "2022-11-28"}, &out)
	if err != nil {
		return nil, err
	}
	return &Ticket{Provider: g.ID(), Key: fmt.Sprintf("#%d", out.Number), URL: out.HTMLURL}, nil
}

// Markdown renders an issue for GitHub (no image upload in its API: the report link shows it).
func Markdown(is *Issue) string {
	var b strings.Builder
	if is.Headline != "" {
		fmt.Fprintf(&b, "**%s**\n\n", is.Headline)
	}
	for _, s := range is.Sections {
		if strings.TrimSpace(s[1]) != "" {
			fmt.Fprintf(&b, "### %s\n%s\n\n", s[0], s[1])
		}
	}
	for _, l := range is.Lists {
		if len(l.Items) == 0 {
			continue
		}
		fmt.Fprintf(&b, "### %s\n", l.Title)
		for _, it := range l.Items {
			fmt.Fprintf(&b, "- %s\n", it)
		}
		b.WriteString("\n")
	}
	for _, c := range is.Code {
		if strings.TrimSpace(c[1]) != "" {
			fmt.Fprintf(&b, "### %s\n```\n%s\n```\n\n", c[0], strings.ReplaceAll(c[1], "```", "'''"))
		}
	}
	if is.Link != "" {
		fmt.Fprintf(&b, "[%s](%s)\n\n", is.LinkText, is.Link)
	}
	if is.Footer != "" {
		fmt.Fprintf(&b, "<sub>%s</sub>\n", is.Footer)
	}
	return b.String()
}

// ---------- Jira ----------

// Jira creates issues in Jira Cloud (email + API token) or Server/Data Center (personal token).
type Jira struct {
	URL, Project, Email, Token, IssueType string
	Labels                                []string
	HTTP                                  *http.Client
}

func (j *Jira) ID() string   { return "jira" }
func (j *Jira) Name() string { return "Jira" }

func (j *Jira) auth() string {
	if j.Email != "" {
		return "Basic " + base64.StdEncoding.EncodeToString([]byte(j.Email+":"+j.Token))
	}
	return "Bearer " + j.Token
}

func (j *Jira) Create(ctx context.Context, is *Issue) (*Ticket, error) {
	base := strings.TrimRight(j.URL, "/")
	fields := map[string]any{"project": map[string]string{"key": j.Project}, "summary": clip(oneLine(is.Title), 250),
		"description": JiraWiki(is), "issuetype": map[string]string{"name": j.IssueType}}
	if len(j.Labels) > 0 {
		labels := make([]string, len(j.Labels))
		for i, l := range j.Labels {
			labels[i] = strings.ReplaceAll(l, " ", "-") // Jira no acepta espacios en un label
		}
		fields["labels"] = labels
	}
	var out struct{ Key string }
	if err := call(ctx, j.HTTP, http.MethodPost, base+"/rest/api/2/issue", map[string]any{"fields": fields},
		map[string]string{"Authorization": j.auth(), "Accept": "application/json"}, &out); err != nil {
		return nil, err
	}
	t := &Ticket{Provider: j.ID(), Key: out.Key, URL: base + "/browse/" + out.Key}
	if len(is.Image) > 0 { // la captura como adjunto; si falla, el ticket ya existe y lleva el link
		_ = j.attach(ctx, base, out.Key, is.Image)
	}
	return t, nil
}

func (j *Jira) attach(ctx context.Context, base, key string, img []byte) error {
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile("file", "screenshot"+imageExt(img))
	fw.Write(img)
	mw.Close()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/rest/api/2/issue/"+url.PathEscape(key)+"/attachments", &body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", j.auth())
	req.Header.Set("X-Atlassian-Token", "no-check")
	return do(j.HTTP, req, nil)
}

// JiraWiki renders an issue in Jira wiki markup (REST API v2 description).
func JiraWiki(is *Issue) string {
	var b strings.Builder
	if is.Headline != "" {
		fmt.Fprintf(&b, "*%s*\n\n", is.Headline)
	}
	for _, s := range is.Sections {
		if strings.TrimSpace(s[1]) != "" {
			fmt.Fprintf(&b, "h3. %s\n%s\n\n", s[0], s[1])
		}
	}
	for _, l := range is.Lists {
		if len(l.Items) == 0 {
			continue
		}
		fmt.Fprintf(&b, "h3. %s\n", l.Title)
		for _, it := range l.Items {
			fmt.Fprintf(&b, "* %s\n", it)
		}
		b.WriteString("\n")
	}
	for _, c := range is.Code {
		if strings.TrimSpace(c[1]) != "" {
			fmt.Fprintf(&b, "h3. %s\n{noformat}\n%s\n{noformat}\n\n", c[0], strings.ReplaceAll(c[1], "{noformat}", "{ noformat}"))
		}
	}
	if is.Link != "" {
		fmt.Fprintf(&b, "[%s|%s]\n\n", is.LinkText, is.Link)
	}
	if is.Footer != "" {
		fmt.Fprintf(&b, "_%s_\n", is.Footer)
	}
	return b.String()
}

// ---------- Azure DevOps ----------

// Azure creates work items in Azure DevOps (Services or Server) with a personal access token.
type Azure struct {
	URL, Project, Token, Type string
	Tags                      []string
	HTTP                      *http.Client
}

func (a *Azure) ID() string   { return "azure" }
func (a *Azure) Name() string { return "Azure DevOps" }

func (a *Azure) auth() string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(":"+a.Token))
}

func (a *Azure) Create(ctx context.Context, is *Issue) (*Ticket, error) {
	base := strings.TrimRight(a.URL, "/") + "/" + url.PathEscape(a.Project) + "/_apis/wit"
	field := "/fields/System.Description"
	if strings.EqualFold(a.Type, "Bug") {
		field = "/fields/Microsoft.VSTS.TCM.ReproSteps" // un Bug muestra los pasos para reproducir, no la descripción
	}
	ops := []map[string]any{
		{"op": "add", "path": "/fields/System.Title", "value": clip(oneLine(is.Title), 250)},
		{"op": "add", "path": field, "value": HTML(is)},
	}
	if len(a.Tags) > 0 {
		ops = append(ops, map[string]any{"op": "add", "path": "/fields/System.Tags", "value": strings.Join(a.Tags, "; ")})
	}
	if len(is.Image) > 0 { // primero se sube la captura y el work item la enlaza
		if u, err := a.upload(ctx, base, is.Image); err == nil {
			ops = append(ops, map[string]any{"op": "add", "path": "/relations/-",
				"value": map[string]any{"rel": "AttachedFile", "url": u, "attributes": map[string]string{"comment": "TraceReports"}}})
		}
	}
	raw, _ := json.Marshal(ops)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/workitems/$"+url.PathEscape(a.Type)+"?api-version=7.1", bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json-patch+json")
	req.Header.Set("Authorization", a.auth())
	var out struct {
		ID    int `json:"id"`
		Links struct {
			HTML struct {
				Href string `json:"href"`
			} `json:"html"`
		} `json:"_links"`
	}
	if err := do(a.HTTP, req, &out); err != nil {
		return nil, err
	}
	return &Ticket{Provider: a.ID(), Key: fmt.Sprintf("%s %d", a.Type, out.ID), URL: out.Links.HTML.Href}, nil
}

func (a *Azure) upload(ctx context.Context, base string, img []byte) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/attachments?fileName=screenshot"+imageExt(img)+"&api-version=7.1", bytes.NewReader(img))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Authorization", a.auth())
	var out struct {
		URL string `json:"url"`
	}
	if err := do(a.HTTP, req, &out); err != nil {
		return "", err
	}
	return out.URL, nil
}

// HTML renders an issue for Azure DevOps (rich text fields are HTML). Everything is escaped.
func HTML(is *Issue) string {
	var b strings.Builder
	e := html.EscapeString
	if is.Headline != "" {
		fmt.Fprintf(&b, "<p><b>%s</b></p>", e(is.Headline))
	}
	for _, s := range is.Sections {
		if strings.TrimSpace(s[1]) != "" {
			fmt.Fprintf(&b, "<h3>%s</h3><p>%s</p>", e(s[0]), strings.ReplaceAll(e(s[1]), "\n", "<br>"))
		}
	}
	for _, l := range is.Lists {
		if len(l.Items) == 0 {
			continue
		}
		fmt.Fprintf(&b, "<h3>%s</h3><ul>", e(l.Title))
		for _, it := range l.Items {
			fmt.Fprintf(&b, "<li>%s</li>", e(it))
		}
		b.WriteString("</ul>")
	}
	for _, c := range is.Code {
		if strings.TrimSpace(c[1]) != "" {
			fmt.Fprintf(&b, "<h3>%s</h3><pre>%s</pre>", e(c[0]), e(c[1]))
		}
	}
	if is.Link != "" {
		fmt.Fprintf(&b, `<p><a href="%s">%s</a></p>`, e(is.Link), e(is.LinkText))
	}
	if is.Footer != "" {
		fmt.Fprintf(&b, "<p><small>%s</small></p>", e(is.Footer))
	}
	return b.String()
}

// ---------- HTTP ----------

func call(ctx context.Context, client *http.Client, method, u string, body any, headers map[string]string, out any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, method, u, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return do(client, req, out)
}

// do sends req and decodes a 2xx JSON answer into out; any other answer is an error with the
// tracker's message (never the request, which carries the token).
func do(client *http.Client, req *http.Request, out any) error {
	if client == nil {
		client = http.DefaultClient
	}
	req.Header.Set("User-Agent", "tracereports")
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("%s: %w", req.URL.Host, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		return fmt.Errorf("%s answered HTTP %d: %s", req.URL.Host, resp.StatusCode, clip(oneLine(string(raw)), 300))
	}
	if out != nil && len(raw) > 0 {
		return json.Unmarshal(raw, out)
	}
	return nil
}

func imageExt(img []byte) string {
	if strings.HasPrefix(http.DetectContentType(img), "image/jpeg") {
		return ".jpg"
	}
	return ".png"
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

func clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}
