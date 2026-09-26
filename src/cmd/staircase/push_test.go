package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseGitHubOwnerRepo(t *testing.T) {
	cases := []struct {
		url   string
		owner string
		repo  string
		ok    bool
	}{
		{"https://github.com/acme/my-repo.git", "acme", "my-repo", true},
		{"https://github.com/acme/my-repo", "acme", "my-repo", true},
		{"git@github.com:acme/my-repo.git", "acme", "my-repo", true},
		{"git@github.com:acme/my-repo", "acme", "my-repo", true},
		// Credentials embedded in the URL (e.g. a token-bearing origin) are still GitHub.
		{"https://user:ghp_secret@github.com/acme/my-repo.git", "acme", "my-repo", true},
		{"https://ghp_secret@github.com/acme/my-repo", "acme", "my-repo", true},
		{"https://gitlab.com/acme/repo.git", "", "", false},
		// Embedded-github.com bypass attempts must NOT be treated as GitHub —
		// this decision gates whether the PAT is attached to the push.
		{"https://evil.example/https://github.com/acme/repo", "", "", false},
		{"https://github.com.evil.example/acme/repo.git", "", "", false},
		{"https://bitbucket.org/acme/repo.git", "", "", false},
		{"/local/path/to/repo", "", "", false},
		{"", "", "", false},
	}
	for _, tc := range cases {
		owner, repo, ok := parseGitHubOwnerRepo(tc.url)
		assert.Equal(t, tc.ok, ok, "url=%q", tc.url)
		if ok {
			assert.Equal(t, tc.owner, owner, "url=%q owner", tc.url)
			assert.Equal(t, tc.repo, repo, "url=%q repo", tc.url)
		}
	}
}

func TestRedactRemote_never_prints_credentials(t *testing.T) {
	for _, url := range []string{
		"https://user:ghp_secret@github.com/acme/repo.git",
		"https://ghp_secret@github.com/acme/repo.git",
		"https://oauth2:ghp_secret@gitlab.com/acme/repo.git",
	} {
		out := redactRemote(url)
		assert.NotContains(t, out, "ghp_secret", "url=%q", url)
		assert.Contains(t, out, "/acme/repo", "url=%q keeps the location", url)
		assert.NotContains(t, manualPRURL(url, "staircase/run-1", "main"), "ghp_secret", "url=%q", url)
	}
	// Non-URL remotes carry no secret and are printed unchanged.
	assert.Equal(t, "git@github.com:acme/repo.git", redactRemote("git@github.com:acme/repo.git"))
	assert.Equal(t, "/srv/git/repo.git", redactRemote("/srv/git/repo.git"))
}
