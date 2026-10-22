package profile_test

import (
	"os"
	"strings"
	"testing"

	"github.com/b070nd/stAirCase/src/internal/profile"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalize_cleans_sorts_and_refuses(t *testing.T) {
	p, err := profile.Profile{
		Protect: []string{" tests/**/*.php ", "phpunit.xml", "phpunit.xml", "./a//b"},
		Scope:   []string{"app/code/**", "app/code/**"},
		Checks:  []string{" vendor/bin/phpunit ", "vendor/bin/phpcs", "vendor/bin/phpunit", ""},
		Sandbox: " docker:image=php:8.2 ",
		Visible: []string{"vendor", "./generated/", "vendor", "node_modules"},
	}.Normalize()
	require.NoError(t, err)
	assert.Equal(t, []string{"a/b", "phpunit.xml", "tests/**/*.php"}, p.Protect)
	assert.Equal(t, []string{"app/code/**"}, p.Scope)
	assert.Equal(t, []string{"vendor/bin/phpunit", "vendor/bin/phpcs"}, p.Checks, "checks keep the order they run in")
	assert.Equal(t, "docker:image=php:8.2", p.Sandbox)
	assert.Equal(t, []string{"generated", "node_modules", "vendor"}, p.Visible)

	for name, bad := range map[string]profile.Profile{
		"an absolute path":          {Protect: []string{"/etc/passwd"}},
		"a path above":              {Scope: []string{"../outside"}},
		"the root itself":           {Sensitive: []string{"."}},
		"a broken pattern":          {Sensitive: []string{"src/[a-"}},
		"a sandbox that is not one": {Sandbox: "gvisor"},
		"a pattern as environment":  {Visible: []string{"vendor/*"}},
		"git's own directory":       {Visible: []string{".git"}},
		"an environment above":      {Visible: []string{"../vendor"}},
	} {
		_, err := bad.Normalize()
		assert.Error(t, err, name)
	}
}

func TestSaveLoad_a_profile_lives_in_the_workspace(t *testing.T) {
	ws := t.TempDir()
	p, found, err := profile.Load(ws, 3)
	require.NoError(t, err)
	assert.False(t, found, "a project with no profile has none")
	assert.True(t, p.Empty())

	require.NoError(t, profile.Save(ws, 3, profile.Profile{Scope: []string{"app/**"}, Checks: []string{"make test"}}))
	got, found, err := profile.Load(ws, 3)
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, []string{"app/**"}, got.Scope)
	assert.NotEqual(t, profile.Profile{}.Digest(), got.Digest())
	_, found, _ = profile.Load(ws, 4)
	assert.False(t, found, "one project's profile is not another's")

	require.NoError(t, profile.Save(ws, 3, profile.Profile{}))
	_, found, err = profile.Load(ws, 3)
	require.NoError(t, err)
	assert.False(t, found, "saving the empty profile removes it")
}

// A profile that cannot be read stops the run: starting under less than the project asks is the one wrong answer.
func TestLoad_refuses_what_it_cannot_read(t *testing.T) {
	ws := t.TempDir()
	require.NoError(t, profile.Save(ws, 1, profile.Profile{Protect: []string{"a"}}))
	require.NoError(t, os.WriteFile(profile.Path(ws, 1), []byte(`{"protect":["a"],"protec":["b"]}`), 0o600))
	_, _, err := profile.Load(ws, 1)
	require.Error(t, err, "a misspelt field would silently drop a protection")
	require.NoError(t, os.WriteFile(profile.Path(ws, 1), []byte(`{not json`), 0o600))
	_, _, err = profile.Load(ws, 1)
	assert.Error(t, err)
	require.NoError(t, os.WriteFile(profile.Path(ws, 1), []byte(`{"scope":["../x"]}`), 0o600))
	_, _, err = profile.Load(ws, 1)
	assert.Error(t, err)
}

func TestDigest_is_of_the_normalized_profile(t *testing.T) {
	a, _ := profile.Profile{Protect: []string{"b", "a"}}.Normalize()
	b, _ := profile.Profile{Protect: []string{"a", "b", "a"}}.Normalize()
	assert.Equal(t, a.Digest(), b.Digest())
}

func TestNormalize_instructions_are_trimmed_and_bounded(t *testing.T) {
	p, err := profile.Profile{Instructions: "  Patches only.\n\n"}.Normalize()
	require.NoError(t, err)
	assert.Equal(t, "Patches only.", p.Instructions)
	_, err = profile.Profile{Instructions: strings.Repeat("x", profile.MaxInstructions+1)}.Normalize()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "at most")
	blank, err := profile.Profile{Instructions: " \n"}.Normalize()
	require.NoError(t, err)
	assert.True(t, blank.Empty(), "blank instructions say nothing")
}
