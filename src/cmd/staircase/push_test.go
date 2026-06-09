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
