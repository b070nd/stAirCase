package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/b070nd/staircase-core/src/internal/domain"
	"github.com/b070nd/staircase-core/src/internal/orchestrator"
	"github.com/b070nd/staircase-core/src/internal/persistence"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var (
	pushGitHubToken string
	pushRemote      string
	pushDraft       bool
)

var pushCmd = &cobra.Command{
	Use:   "push <run-id>",
	Short: "Push a completed run's branch and open a GitHub PR",
	Long: `Push the staircase/run-{ID} branch to the remote and open a GitHub
pull request targeting the base branch recorded for that run.

A GitHub personal access token is required to create the PR. Pass it via
--github-token or the GITHUB_TOKEN environment variable. The token needs
the 'repo' scope.

If the remote is not github.com the branch is still pushed but PR creation
is skipped and the PR URL is printed for manual use.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		runID, err := strconv.ParseInt(args[0], 10, 64)
		if err != nil {
			return fmt.Errorf("invalid run-id %q: %w", args[0], err)
		}

		wsDir := viper.GetString("STAIRCASE_DIR")
		token := pushGitHubToken
		if token == "" {
			token = os.Getenv("GITHUB_TOKEN")
		}

		// ── Load run record ───────────────────────────────────────────────────
		db, err := persistence.InitDB(wsDir)
		if err != nil {
			return fmt.Errorf("open workspace db: %w", err)
		}
		defer func() { _ = db.Close() }()
		store := persistence.NewStore(db)

		run, err := store.GetRun(runID)
		if err != nil {
			return fmt.Errorf("load run: %w", err)
		}
		if run == nil {
			return fmt.Errorf("run %d not found", runID)
		}
		if run.Status != domain.RunStatusSuccess {
			return fmt.Errorf("run %d status is %q — only successful runs can be pushed", runID, run.Status)
		}
		if run.GitCommitHash == "" {
			return fmt.Errorf("run %d has no commit hash — nothing to push", runID)
		}

		// ── Open git repo ─────────────────────────────────────────────────────
		caseRec, err := store.GetCase(run.CaseID)
		if err != nil || caseRec == nil {
			return fmt.Errorf("load case %d: %v", run.CaseID, err)
		}
		proj, err := store.GetProject(caseRec.ProjectID)
		if err != nil || proj == nil {
			return fmt.Errorf("load project %d: %v", caseRec.ProjectID, err)
		}

		gr, err := orchestrator.OpenGitRepo(proj.SourcePath)
		if err != nil {
			return fmt.Errorf("open git repo %s: %w", proj.SourcePath, err)
		}

		// ── Push branch ───────────────────────────────────────────────────────
		runBranch := fmt.Sprintf("staircase/run-%d", runID)
		remote := pushRemote
		if remote == "" {
			remote = "origin"
		}

		// Resolve the remote URL before pushing: the GitHub token must only
		// ever be attached to a github.com remote, otherwise a misconfigured
		// or malicious remote URL receives the credential in BasicAuth.
		remoteURL, err := gr.RemoteURL(remote)
		if err != nil {
			return fmt.Errorf("read remote %q URL: %w", remote, err)
		}
		owner, repo, isGitHub := parseGitHubOwnerRepo(remoteURL)
		pushToken := token
		if !isGitHub && pushToken != "" {
			fmt.Printf("   ⚠️  Remote %q is not github.com — pushing without the GitHub token\n", remoteURL)
			pushToken = ""
		}

		fmt.Printf("⬆️  Pushing %s → %s/%s …\n", runBranch, remote, runBranch)
		if err := gr.Push(remote, runBranch, pushToken); err != nil {
			return fmt.Errorf("push failed: %w", err)
		}
		fmt.Println("✅ Branch pushed.")

		// ── GitHub PR creation ────────────────────────────────────────────────
		if !isGitHub {
			fmt.Printf("   ℹ️  Remote %q is not a recognized GitHub URL — create the PR manually:\n", remoteURL)
			fmt.Printf("   %s\n", manualPRURL(remoteURL, runBranch, run.GitBranch))
			return nil
		}
		if token == "" {
			fmt.Printf("   ℹ️  No --github-token provided — create the PR manually:\n")
			fmt.Printf("   https://github.com/%s/%s/compare/%s?expand=1\n", owner, repo, runBranch)
			return nil
		}

		prTitle := fmt.Sprintf("staircase: run #%d (case #%d)", runID, run.CaseID)
		prBody := fmt.Sprintf(
			"Automated agent run [#%d](../../runs/%d) on case %d.\n\nCommit: `%s`",
			runID, runID, run.CaseID, run.GitCommitHash,
		)
		prURL, err := createGitHubPR(token, owner, repo, runBranch, run.GitBranch, prTitle, prBody, pushDraft)
		if err != nil {
			return fmt.Errorf("create PR: %w", err)
		}
		fmt.Printf("🎉 Pull request opened: %s\n", prURL)

		// Post a commit status so the PR shows the staircase check green.
		if postErr := postGitHubStatus(token, owner, repo, run.GitCommitHash, "success",
			fmt.Sprintf("Agent run #%d completed", runID)); postErr != nil {
			fmt.Printf("   ⚠️  Could not post commit status: %v\n", postErr)
		}
		return nil
	},
}

// ─── helpers ─────────────────────────────────────────────────────────────────

// parseGitHubOwnerRepo extracts owner and repo from common GitHub remote URL
// formats. Returns ok=false when the URL is not a github.com remote.
//
//	https://github.com/owner/repo.git → owner, repo, true
//	git@github.com:owner/repo.git     → owner, repo, true
//
// Anchored (^) so a malicious URL embedding "github.com" in its path
// (e.g. https://evil.example/https://github.com/o/r) is never treated as
// a GitHub remote — that decision also gates token attachment on push.
var reGitHubHTTPS = regexp.MustCompile(`^(?i)https?://github\.com/([^/]+)/([^/.]+)`)
var reGitHubSSH = regexp.MustCompile(`^(?i)git@github\.com:([^/]+)/([^/.]+)`)

func parseGitHubOwnerRepo(remoteURL string) (owner, repo string, ok bool) {
	for _, re := range []*regexp.Regexp{reGitHubHTTPS, reGitHubSSH} {
		if m := re.FindStringSubmatch(remoteURL); m != nil {
			return m[1], m[2], true
		}
	}
	return "", "", false
}

// manualPRURL returns a best-effort PR comparison URL for non-GitHub remotes.
func manualPRURL(remoteURL, head, base string) string {
	// GitLab: https://gitlab.com/owner/repo/-/merge_requests/new?...
	if strings.Contains(remoteURL, "gitlab.com") {
		u := strings.TrimSuffix(remoteURL, ".git")
		return fmt.Sprintf("%s/-/merge_requests/new?merge_request[source_branch]=%s&merge_request[target_branch]=%s", u, head, base)
	}
	return fmt.Sprintf("%s (push branch %s and open a PR against %s)", remoteURL, head, base)
}

// createGitHubPR opens a pull request via the GitHub REST API.
func createGitHubPR(token, owner, repo, head, base, title, body string, draft bool) (string, error) {
	payload, _ := json.Marshal(map[string]any{
		"title": title,
		"head":  head,
		"base":  base,
		"body":  body,
		"draft": draft,
	})
	req, err := http.NewRequest(http.MethodPost,
		fmt.Sprintf("https://api.github.com/repos/%s/%s/pulls", owner, repo),
		bytes.NewReader(payload),
	)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("github api: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("github api returned %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	var result struct {
		HTMLURL string `json:"html_url"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("parse github response: %w", err)
	}
	return result.HTMLURL, nil
}

// postGitHubStatus posts a commit status check on the given SHA.
func postGitHubStatus(token, owner, repo, sha, state, description string) error {
	payload, _ := json.Marshal(map[string]string{
		"state":       state,
		"context":     "staircase/run",
		"description": description,
	})
	req, err := http.NewRequest(http.MethodPost,
		fmt.Sprintf("https://api.github.com/repos/%s/%s/statuses/%s", owner, repo, sha),
		bytes.NewReader(payload),
	)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("status %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return nil
}

func init() {
	pushCmd.Flags().StringVar(&pushGitHubToken, "github-token", "", "GitHub PAT (default: $GITHUB_TOKEN)")
	pushCmd.Flags().StringVar(&pushRemote, "remote", "origin", "Git remote name")
	pushCmd.Flags().BoolVar(&pushDraft, "draft", false, "Open PR as a draft")
	rootCmd.AddCommand(pushCmd)
}
