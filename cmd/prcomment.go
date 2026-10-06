package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/josemiguellopez/tracereports/internal/offline"
	"github.com/josemiguellopez/tracereports/internal/prcomment"
)

const prCommentUsage = `Usage: tracereports pr-comment [flags]

Comments the run's summary on the pull request (GitHub Actions) or merge request (GitLab CI) of
the current job: failures, what changed against the previous run, incidents with their likely
cause and the report link. The next push updates the same comment instead of adding another.

GitHub: GITHUB_TOKEN with "permissions: pull-requests: write". GitLab: GITLAB_TOKEN (api scope).
Outside a pull request it prints the comment (and adds it to the GitHub job summary).

Flags:
`

// runPRComment implements "tracereports pr-comment".
func runPRComment(args []string) error {
	fl := flag.NewFlagSet("pr-comment", flag.ContinueOnError)
	fl.Usage = func() { fmt.Fprint(fl.Output(), prCommentUsage); fl.PrintDefaults() }
	base := fl.String("url", envOr("TRACEREPORTS_URL", "http://localhost:8080"), "server URL (default $TRACEREPORTS_URL)")
	token := fl.String("token", os.Getenv("TRACEREPORTS_TOKEN"), "API token (default $TRACEREPORTS_TOKEN)")
	runID := fl.Int64("run", 0, "run id (default: the latest run of this commit)")
	from := fl.String("from", "", "without a server: a recording folder, JUnit XML or allure-results to summarize")
	name := fl.String("name", "", "run name for --from JUnit XML or Allure inputs")
	lang := fl.String("lang", "", "es or en (default: the server's language, or en)")
	link := fl.String("link", "", "report link (default: the server report; with --from, none)")
	wait := fl.Duration("wait", 90*time.Second, "how long to wait for the run diagnosis to finish")
	dryRun := fl.Bool("dry-run", false, "print the comment without posting it")
	if _, err := parseInterspersed(fl, args); err != nil {
		return err
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})))

	var target offline.Target
	reportLink := *link
	if *from != "" {
		ls, err := newLocalServer(false)
		if err != nil {
			return err
		}
		defer ls.close()
		q := url.Values{}
		if *name != "" {
			q.Set("name", *name)
		}
		ids, err := loadInput(ls.target, *from, q)
		if err != nil {
			return fmt.Errorf("%s: %w", *from, err)
		}
		if len(ids) == 0 {
			return errors.New("the input did not create any run")
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		ls.analyzer.Wait(ctx)
		cancel()
		target, *runID = ls.target, ids[len(ids)-1]
	} else {
		target = offline.Target{Doer: &http.Client{Timeout: 30 * time.Second}, BaseURL: strings.TrimRight(*base, "/"), Token: *token}
		if *runID == 0 {
			id, err := runOfCommit(target)
			if err != nil {
				return err
			}
			*runID = id
		}
		if reportLink == "" {
			public := strings.TrimRight(os.Getenv("PUBLIC_URL"), "/")
			if public == "" {
				public = target.BaseURL
			}
			reportLink = fmt.Sprintf("%s/#run=%d&view=dashboard", public, *runID)
		}
	}

	run, err := waitSummary(target, *runID, *wait)
	if err != nil {
		return err
	}
	var cmp prcomment.Comparison
	if err := getJSON(target, fmt.Sprintf("/api/v1/runs/%d/compare", *runID), &cmp); err != nil {
		return err
	}
	if *lang == "" {
		var cfg struct {
			Language string `json:"language"`
		}
		_ = getJSON(target, "/api/v1/config", &cfg)
		*lang = cfg.Language
	}
	body := prcomment.Markdown(run, &cmp, *lang, reportLink)

	if p := os.Getenv("GITHUB_STEP_SUMMARY"); p != "" && !*dryRun {
		if f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
			f.WriteString(body + "\n")
			f.Close()
		}
	}
	platform, err := prcomment.Detect(os.Getenv, &http.Client{Timeout: 30 * time.Second})
	if err != nil {
		return err
	}
	if *dryRun || platform == nil {
		fmt.Println(body)
		if platform == nil && !*dryRun {
			fmt.Fprintln(os.Stderr, "Not a pull request (GitHub Actions or GitLab CI merge request): printed the comment instead.")
		}
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	u, err := platform.Upsert(ctx, prcomment.Marker(run), body)
	if err != nil {
		return fmt.Errorf("%s: %w", platform.Name(), err)
	}
	fmt.Printf("Comment on %s: %s\n", platform.Name(), u)
	return nil
}

// runOfCommit finds the latest run of the commit being built. The clients report
// TRACEREPORTS_COMMIT, GITHUB_SHA or CI_COMMIT_SHA; in a GitHub pull request GITHUB_SHA is the
// merge commit, so the PR head is accepted too. Without any commit, the latest run.
func runOfCommit(t offline.Target) (int64, error) {
	var runs []struct {
		ID     int64  `json:"id"`
		Commit string `json:"commit"`
	}
	if err := getJSON(t, "/api/v1/runs?limit=200", &runs); err != nil {
		return 0, err
	}
	var shas []string
	for _, v := range []string{os.Getenv("TRACEREPORTS_COMMIT"), os.Getenv("GITHUB_SHA"), os.Getenv("CI_COMMIT_SHA"), prHead()} {
		if v != "" {
			shas = append(shas, v)
		}
	}
	for _, r := range runs { // vienen de la más nueva a la más vieja
		if len(shas) == 0 {
			return r.ID, nil
		}
		for _, sha := range shas {
			if sameCommit(r.Commit, sha) {
				return r.ID, nil
			}
		}
	}
	if len(shas) == 0 {
		return 0, errors.New("the server has no runs")
	}
	return 0, fmt.Errorf("no run for commit %s on the server; pass --run", shas[0])
}

// sameCommit compares full or abbreviated (7+ characters) SHAs.
func sameCommit(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	if len(a) > len(b) {
		a, b = b, a
	}
	return len(a) >= 7 && strings.HasPrefix(b, a)
}

// prHead is the head commit of the GitHub pull request being built ("" outside one).
func prHead() string {
	raw, err := os.ReadFile(os.Getenv("GITHUB_EVENT_PATH"))
	if err != nil {
		return ""
	}
	var ev struct {
		PullRequest struct {
			Head struct {
				SHA string `json:"sha"`
			} `json:"head"`
		} `json:"pull_request"`
	}
	_ = json.Unmarshal(raw, &ev)
	return ev.PullRequest.Head.SHA
}

// waitSummary returns the run once its diagnosis is written (or after wait).
func waitSummary(t offline.Target, id int64, wait time.Duration) (*prcomment.Run, error) {
	deadline := time.Now().Add(wait)
	for {
		var run prcomment.Run
		if err := getJSON(t, "/api/v1/runs/"+strconv.FormatInt(id, 10), &run); err != nil {
			return nil, err
		}
		pending := run.Status == "RUNNING" || (run.Failed > 0 && (run.Summary == nil || run.Summary.State == "PENDING"))
		if !pending || time.Now().After(deadline) {
			return &run, nil
		}
		time.Sleep(3 * time.Second)
	}
}

// getJSON reads an API path of t.
func getJSON(t offline.Target, path string, out any) error {
	req, err := http.NewRequest(http.MethodGet, t.BaseURL+path, nil)
	if err != nil {
		return err
	}
	if t.Token != "" {
		req.Header.Set("Authorization", "Bearer "+t.Token)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := t.Doer.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: HTTP %d %s", path, resp.StatusCode, apiError(raw))
	}
	return json.Unmarshal(raw, out)
}
