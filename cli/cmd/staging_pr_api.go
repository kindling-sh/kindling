package cmd

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ════════════════════════════════════════════════════════════════
// C6 — PR-preview / promote dashboard UI
//
// Surfaces what B2 (kindling-pr-preview) and B3 (kindling-gitops-handoff)
// already produce as composite GH Actions: open PRs with a staging
// preview deploy, a diff of what would be promoted, and a manual
// "promote now" trigger. This file never talks to a live Kubernetes
// cluster and never touches the GitOps repo directly -- it only reads
// GitHub's own REST API (PRs, Actions artifacts) and, for promotion,
// dispatches the same new promote-pr.yml workflow a real PR merge's
// automatic kindling-gitops-handoff run already uses -- see that
// workflow file's own header comment for why this item deliberately
// never reimplements the artifact-fetch/GitOps-commit logic itself.
// ════════════════════════════════════════════════════════════════

// ── Session config (token, owner/repo, GitOps target) ───────────
//
// Session-only, mirroring stagingContext's own pattern exactly (see
// staging_dashboard_api.go) -- not persisted anywhere, so a dashboard
// restart always starts over. Owner/repo are optional overrides: left
// blank, they're auto-detected from the current working directory's own
// `git remote get-url origin` on every request, so the common case (the
// dashboard is running inside the same repo it's managing) needs zero
// configuration at all.

type githubPRConfig struct {
	Token            string // GH PAT / fine-grained token; never echoed back by the GET endpoint
	Owner            string // override; blank = auto-detect from git remote
	Repo             string // override; blank = auto-detect from git remote
	GitOpsRepo       string // "owner/repo"; blank = same as Owner/Repo above
	GitOpsPath       string // default "environments/production"
	GitOpsBaseBranch string // default "main"
	Workflow         string // promote-pr workflow filename; default "promote-pr.yml"
}

var (
	githubConfigMu sync.RWMutex
	githubConfig   githubPRConfig
)

func getGitHubPRConfig() githubPRConfig {
	githubConfigMu.RLock()
	defer githubConfigMu.RUnlock()
	return githubConfig
}

func setGitHubPRConfig(cfg githubPRConfig) {
	githubConfigMu.Lock()
	githubConfig = cfg
	githubConfigMu.Unlock()
}

// githubOwnerRepo resolves the (owner, repo) this dashboard manages:
// an explicit config override always wins, otherwise auto-detected from
// `git remote get-url origin` run in the dashboard process's own working
// directory (the same detection style already used by `kindling analyze`
// -- see analyze.go -- just also parsed into owner/repo components here,
// since the GitHub API needs them separately).
func githubOwnerRepo() (owner, repo string, err error) {
	cfg := getGitHubPRConfig()
	if cfg.Owner != "" && cfg.Repo != "" {
		return cfg.Owner, cfg.Repo, nil
	}
	out, cmdErr := exec.Command("git", "remote", "get-url", "origin").Output()
	if cmdErr != nil {
		return "", "", fmt.Errorf("no owner/repo configured and no git remote 'origin' found: %w", cmdErr)
	}
	owner, repo, ok := parseGitHubOwnerRepo(strings.TrimSpace(string(out)))
	if !ok {
		return "", "", fmt.Errorf("git remote 'origin' (%s) is not a recognizable GitHub URL -- configure owner/repo explicitly", strings.TrimSpace(string(out)))
	}
	return owner, repo, nil
}

// parseGitHubOwnerRepo extracts "owner", "repo" from either remote URL
// style git supports: "git@github.com:owner/repo.git" (SSH) or
// "https://github.com/owner/repo.git" (HTTPS, .git suffix optional).
func parseGitHubOwnerRepo(remote string) (owner, repo string, ok bool) {
	remote = strings.TrimSuffix(remote, ".git")
	var path string
	switch {
	case strings.HasPrefix(remote, "git@github.com:"):
		path = strings.TrimPrefix(remote, "git@github.com:")
	case strings.HasPrefix(remote, "https://github.com/"):
		path = strings.TrimPrefix(remote, "https://github.com/")
	case strings.HasPrefix(remote, "http://github.com/"):
		path = strings.TrimPrefix(remote, "http://github.com/")
	default:
		return "", "", false
	}
	parts := strings.SplitN(path, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

// gitopsTarget resolves which repo/path/branch "promote" commits into,
// matching kindling-gitops-handoff's own input defaults exactly
// (gitops-repo blank -> current repo, gitops-path default
// "environments/production", gitops-base-branch default "main") so a
// dashboard user never has to re-specify something the workflow already
// defaults sensibly.
func gitopsTarget() (owner, repo, path, branch string, err error) {
	cfg := getGitHubPRConfig()
	if cfg.GitOpsRepo != "" {
		o, r, ok := splitOwnerRepo(cfg.GitOpsRepo)
		if !ok {
			return "", "", "", "", fmt.Errorf("gitops_repo %q is not in \"owner/repo\" form", cfg.GitOpsRepo)
		}
		owner, repo = o, r
	} else {
		owner, repo, err = githubOwnerRepo()
		if err != nil {
			return "", "", "", "", err
		}
	}
	path = cfg.GitOpsPath
	if path == "" {
		path = "environments/production"
	}
	branch = cfg.GitOpsBaseBranch
	if branch == "" {
		branch = "main"
	}
	return owner, repo, path, branch, nil
}

func splitOwnerRepo(s string) (owner, repo string, ok bool) {
	parts := strings.SplitN(s, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

func promoteWorkflowFile() string {
	cfg := getGitHubPRConfig()
	if cfg.Workflow != "" {
		return cfg.Workflow
	}
	return "promote-pr.yml"
}

// ── GitHub API client (minimal, stdlib-only) ────────────────────

var githubHTTPClient = &http.Client{Timeout: 30 * time.Second}

// githubAPI issues an authenticated request against api.github.com.
// path must start with "/" (e.g. "/repos/owner/repo/pulls"). Returns the
// raw response for callers that need the body/headers (e.g. artifact
// downloads follow a redirect themselves); json-decoding callers should
// use githubAPIJSON instead.
func githubAPI(method, path string, body io.Reader) (*http.Response, error) {
	token := getGitHubPRConfig().Token
	if token == "" {
		return nil, fmt.Errorf("no GitHub token configured -- set one via POST /api/staging/github/config first")
	}
	req, err := http.NewRequest(method, "https://api.github.com"+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return githubHTTPClient.Do(req)
}

// githubAPIJSON issues a request and decodes a 2xx JSON response into v.
func githubAPIJSON(method, path string, body io.Reader, v interface{}) error {
	resp, err := githubAPI(method, path, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("GitHub API %s %s: %d %s", method, path, resp.StatusCode, strings.TrimSpace(string(data)))
	}
	if v == nil {
		return nil
	}
	return json.Unmarshal(data, v)
}

// ── /api/staging/github/config — session GH config ──────────────

func handleGitHubPRConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		cfg := getGitHubPRConfig()
		owner, repo, _ := githubOwnerRepo()
		jsonResponse(w, map[string]interface{}{
			"configured":         cfg.Token != "",
			"owner":              owner,
			"repo":               repo,
			"owner_override":     cfg.Owner,
			"repo_override":      cfg.Repo,
			"gitops_repo":        cfg.GitOpsRepo,
			"gitops_path":        cfg.GitOpsPath,
			"gitops_base_branch": cfg.GitOpsBaseBranch,
			"workflow":           promoteWorkflowFile(),
		})
	case http.MethodPost:
		var body struct {
			Token            string `json:"token"`
			Owner            string `json:"owner"`
			Repo             string `json:"repo"`
			GitOpsRepo       string `json:"gitops_repo"`
			GitOpsPath       string `json:"gitops_path"`
			GitOpsBaseBranch string `json:"gitops_base_branch"`
			Workflow         string `json:"workflow"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			jsonError(w, "invalid request body", 400)
			return
		}
		cfg := getGitHubPRConfig()
		// A blank token in the request leaves the previously-configured
		// one in place -- so a user editing just the owner/repo override
		// doesn't have to re-paste their token every time.
		if body.Token != "" {
			cfg.Token = body.Token
		}
		cfg.Owner = body.Owner
		cfg.Repo = body.Repo
		cfg.GitOpsRepo = body.GitOpsRepo
		cfg.GitOpsPath = body.GitOpsPath
		cfg.GitOpsBaseBranch = body.GitOpsBaseBranch
		cfg.Workflow = body.Workflow
		setGitHubPRConfig(cfg)
		jsonResponse(w, map[string]interface{}{"ok": true})
	default:
		jsonError(w, "method not allowed", 405)
	}
}

// ── Artifact lookup (shared by the PR list and diff handlers) ───

type ghArtifact struct {
	ID                 int64  `json:"id"`
	Name               string `json:"name"`
	Expired            bool   `json:"expired"`
	CreatedAt          string `json:"created_at"`
	ArchiveDownloadURL string `json:"archive_download_url"`
}

// listArtifacts fetches up to 100 of the most recent artifacts for the
// repo. Single-page only -- a known, deliberate limitation (see C6's
// spec), acceptable since artifact retention is time-bounded anyway and
// the ones this feature cares about (this PR's latest preview) are
// always recent.
func listArtifacts(owner, repo string) ([]ghArtifact, error) {
	var result struct {
		Artifacts []ghArtifact `json:"artifacts"`
	}
	path := fmt.Sprintf("/repos/%s/%s/actions/artifacts?per_page=100", owner, repo)
	if err := githubAPIJSON(http.MethodGet, path, nil, &result); err != nil {
		return nil, err
	}
	return result.Artifacts, nil
}

// latestArtifactForPR returns the most recent non-expired artifact named
// "values-<prNumber>", or nil if none exists yet.
func latestArtifactForPR(artifacts []ghArtifact, prNumber int) *ghArtifact {
	want := fmt.Sprintf("values-%d", prNumber)
	var best *ghArtifact
	for i := range artifacts {
		a := &artifacts[i]
		if a.Name != want || a.Expired {
			continue
		}
		if best == nil || a.CreatedAt > best.CreatedAt {
			best = a
		}
	}
	return best
}

// ── /api/staging/prs — open PRs with a preview artifact ──────────

func handleStagingPRs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, "method not allowed", 405)
		return
	}
	owner, repo, err := githubOwnerRepo()
	if err != nil {
		jsonError(w, err.Error(), 400)
		return
	}

	var prs []struct {
		Number  int    `json:"number"`
		Title   string `json:"title"`
		HTMLURL string `json:"html_url"`
		Head    struct {
			Ref string `json:"ref"`
		} `json:"head"`
	}
	if err := githubAPIJSON(http.MethodGet, fmt.Sprintf("/repos/%s/%s/pulls?state=open&per_page=100", owner, repo), nil, &prs); err != nil {
		jsonError(w, "failed to list pull requests: "+err.Error(), 502)
		return
	}

	artifacts, err := listArtifacts(owner, repo)
	if err != nil {
		jsonError(w, "failed to list artifacts: "+err.Error(), 502)
		return
	}

	type prInfo struct {
		Number            int    `json:"number"`
		Title             string `json:"title"`
		Branch            string `json:"branch"`
		URL               string `json:"url"`
		HasPreview        bool   `json:"has_preview"`
		ArtifactCreatedAt string `json:"artifact_created_at,omitempty"`
	}
	results := make([]prInfo, 0, len(prs))
	for _, pr := range prs {
		info := prInfo{Number: pr.Number, Title: pr.Title, Branch: pr.Head.Ref, URL: pr.HTMLURL}
		if a := latestArtifactForPR(artifacts, pr.Number); a != nil {
			info.HasPreview = true
			info.ArtifactCreatedAt = a.CreatedAt
		}
		results = append(results, info)
	}
	// Most-recently-previewed first, PRs with no preview at all last.
	sort.Slice(results, func(i, j int) bool {
		if results[i].HasPreview != results[j].HasPreview {
			return results[i].HasPreview
		}
		return results[i].ArtifactCreatedAt > results[j].ArtifactCreatedAt
	})

	jsonResponse(w, map[string]interface{}{"prs": results})
}

// ── /api/staging/prs/{number}/diff ───────────────────────────────

func handleStagingPRDiff(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, "method not allowed", 405)
		return
	}
	parts, err := parsePathParams(r.URL.Path, "/api/staging/prs/", 2)
	if err != nil || parts[1] != "diff" {
		jsonError(w, "usage: /api/staging/prs/{number}/diff", 400)
		return
	}
	prNumber, err := strconv.Atoi(parts[0])
	if err != nil {
		jsonError(w, "invalid PR number", 400)
		return
	}

	owner, repo, err := githubOwnerRepo()
	if err != nil {
		jsonError(w, err.Error(), 400)
		return
	}
	artifacts, err := listArtifacts(owner, repo)
	if err != nil {
		jsonError(w, "failed to list artifacts: "+err.Error(), 502)
		return
	}
	artifact := latestArtifactForPR(artifacts, prNumber)
	if artifact == nil {
		jsonError(w, fmt.Sprintf("no preview artifact found for PR #%d yet", prNumber), 404)
		return
	}

	newValues, err := fetchValuesProdFromArtifact(owner, repo, artifact.ID)
	if err != nil {
		jsonError(w, "failed to read preview artifact: "+err.Error(), 502)
		return
	}

	gitopsOwner, gitopsRepo, gitopsPath, gitopsBranch, err := gitopsTarget()
	if err != nil {
		jsonError(w, err.Error(), 400)
		return
	}
	oldValues, err := fetchGitOpsFile(gitopsOwner, gitopsRepo, gitopsPath, gitopsBranch)
	if err != nil {
		// A missing file in the GitOps repo (first-ever promotion) is not
		// an error -- diff against an empty "before" instead.
		oldValues = ""
	}

	jsonResponse(w, map[string]interface{}{
		"pr_number":  prNumber,
		"old":        oldValues,
		"new":        newValues,
		"diff_lines": diffLines(oldValues, newValues),
	})
}

// fetchValuesProdFromArtifact downloads a GH Actions artifact zip and
// extracts values-prod.yaml from it. http.Client follows the API's 302
// redirect to the signed download URL automatically for GET requests.
func fetchValuesProdFromArtifact(owner, repo string, artifactID int64) (string, error) {
	resp, err := githubAPI(http.MethodGet, fmt.Sprintf("/repos/%s/%s/actions/artifacts/%d/zip", owner, repo, artifactID), nil)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("download failed: %d %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}

	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", fmt.Errorf("artifact is not a valid zip: %w", err)
	}
	for _, f := range zr.File {
		if f.Name != "values-prod.yaml" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return "", err
		}
		defer rc.Close()
		content, err := io.ReadAll(rc)
		if err != nil {
			return "", err
		}
		return string(content), nil
	}
	return "", fmt.Errorf("values-prod.yaml not found in artifact")
}

// fetchGitOpsFile reads <path>/values.yaml from the GitOps repo via the
// Contents API, matching kindling-gitops-handoff's own destination file.
func fetchGitOpsFile(owner, repo, path, branch string) (string, error) {
	var result struct {
		Content  string `json:"content"`
		Encoding string `json:"encoding"`
	}
	apiPath := fmt.Sprintf("/repos/%s/%s/contents/%s/values.yaml?ref=%s", owner, repo, strings.Trim(path, "/"), branch)
	if err := githubAPIJSON(http.MethodGet, apiPath, nil, &result); err != nil {
		return "", err
	}
	if result.Encoding != "base64" {
		return result.Content, nil
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(result.Content, "\n", ""))
	if err != nil {
		return "", err
	}
	return string(decoded), nil
}

// ── /api/staging/prs/{number}/promote ─────────────────────────────

func handleStagingPRPromote(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, "method not allowed", 405)
		return
	}
	parts, err := parsePathParams(r.URL.Path, "/api/staging/prs/", 2)
	if err != nil || parts[1] != "promote" {
		jsonError(w, "usage: /api/staging/prs/{number}/promote", 400)
		return
	}
	prNumber, err := strconv.Atoi(parts[0])
	if err != nil {
		jsonError(w, "invalid PR number", 400)
		return
	}

	owner, repo, err := githubOwnerRepo()
	if err != nil {
		jsonError(w, err.Error(), 400)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, ok := w.(http.Flusher)
	if !ok {
		jsonError(w, "streaming not supported", 500)
		return
	}
	send := func(msgType, msg string) {
		fmt.Fprintf(w, "data: %s\n\n", mustJSON(map[string]string{"type": msgType, "message": msg}))
		flusher.Flush()
	}

	workflow := promoteWorkflowFile()

	// Default branch -- workflow_dispatch requires a ref to run against;
	// the workflow file itself only needs to exist on that ref, the
	// pr_number input is what determines which PR actually gets promoted.
	var repoInfo struct {
		DefaultBranch string `json:"default_branch"`
	}
	if err := githubAPIJSON(http.MethodGet, fmt.Sprintf("/repos/%s/%s", owner, repo), nil, &repoInfo); err != nil {
		send("error", "failed to look up default branch: "+err.Error())
		return
	}

	dispatchedAt := time.Now().UTC()
	payload, _ := json.Marshal(map[string]interface{}{
		"ref":    repoInfo.DefaultBranch,
		"inputs": map[string]string{"pr_number": strconv.Itoa(prNumber)},
	})
	send("step", fmt.Sprintf("Dispatching %s for PR #%d", workflow, prNumber))
	dispatchPath := fmt.Sprintf("/repos/%s/%s/actions/workflows/%s/dispatches", owner, repo, workflow)
	if err := githubAPIJSON(http.MethodPost, dispatchPath, bytes.NewReader(payload), nil); err != nil {
		send("error", "failed to dispatch promote workflow: "+err.Error())
		return
	}

	// workflow_dispatch's own API response never includes the run it
	// just created, so the only way to find it is to poll the workflow's
	// recent runs until one appears with a created_at after the moment
	// we dispatched -- the same "poll until a new thing shows up"
	// approach already used elsewhere in this codebase for cert-manager/
	// ClusterIssuer readiness (see handleStagingTLSInstall).
	send("step", "Waiting for the dispatched run to start")
	runsPath := fmt.Sprintf("/repos/%s/%s/actions/workflows/%s/runs?event=workflow_dispatch&per_page=5", owner, repo, workflow)
	var runID int64
	for i := 0; i < 20 && runID == 0; i++ {
		var runsResp struct {
			WorkflowRuns []struct {
				ID        int64  `json:"id"`
				CreatedAt string `json:"created_at"`
			} `json:"workflow_runs"`
		}
		if err := githubAPIJSON(http.MethodGet, runsPath, nil, &runsResp); err == nil {
			for _, run := range runsResp.WorkflowRuns {
				createdAt, perr := time.Parse(time.RFC3339, run.CreatedAt)
				if perr == nil && createdAt.After(dispatchedAt.Add(-5*time.Second)) {
					runID = run.ID
					break
				}
			}
		}
		if runID == 0 {
			time.Sleep(3 * time.Second)
		}
	}
	if runID == 0 {
		send("error", "dispatched the workflow but could not find its run -- check the Actions tab directly")
		return
	}
	send("step", fmt.Sprintf("Run started (id %d) — waiting for it to finish", runID))

	for i := 0; i < 100; i++ {
		var run struct {
			Status     string `json:"status"`
			Conclusion string `json:"conclusion"`
			HTMLURL    string `json:"html_url"`
		}
		if err := githubAPIJSON(http.MethodGet, fmt.Sprintf("/repos/%s/%s/actions/runs/%d", owner, repo, runID), nil, &run); err != nil {
			send("error", "failed to poll run status: "+err.Error())
			return
		}
		if run.Status == "completed" {
			if run.Conclusion == "success" {
				send("done", fmt.Sprintf("Promoted PR #%d — %s", prNumber, run.HTMLURL))
			} else {
				send("error", fmt.Sprintf("Promote run finished with conclusion %q — %s", run.Conclusion, run.HTMLURL))
			}
			return
		}
		time.Sleep(3 * time.Second)
	}
	send("error", "timed out waiting for the promote run to finish — check the Actions tab directly")
}

// ── Minimal line diff (LCS-based) ────────────────────────────────
//
// Good enough for a config YAML file's size (a handful to a few hundred
// lines) -- O(n*m) time/space is irrelevant at that scale. Not meant to
// be a general-purpose diff library; this repo has no existing diff
// dependency and doesn't need one just for this.

type diffLine struct {
	Type string `json:"type"` // "same", "add", "remove"
	Text string `json:"text"`
}

func diffLines(oldText, newText string) []diffLine {
	oldLines := splitLines(oldText)
	newLines := splitLines(newText)
	n, m := len(oldLines), len(newLines)

	// lcs[i][j] = length of the longest common subsequence of
	// oldLines[i:] and newLines[j:].
	lcs := make([][]int, n+1)
	for i := range lcs {
		lcs[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if oldLines[i] == newLines[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else if lcs[i+1][j] >= lcs[i][j+1] {
				lcs[i][j] = lcs[i+1][j]
			} else {
				lcs[i][j] = lcs[i][j+1]
			}
		}
	}

	var result []diffLine
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case oldLines[i] == newLines[j]:
			result = append(result, diffLine{"same", oldLines[i]})
			i++
			j++
		case lcs[i+1][j] >= lcs[i][j+1]:
			result = append(result, diffLine{"remove", oldLines[i]})
			i++
		default:
			result = append(result, diffLine{"add", newLines[j]})
			j++
		}
	}
	for ; i < n; i++ {
		result = append(result, diffLine{"remove", oldLines[i]})
	}
	for ; j < m; j++ {
		result = append(result, diffLine{"add", newLines[j]})
	}
	return result
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimRight(s, "\n"), "\n")
}
