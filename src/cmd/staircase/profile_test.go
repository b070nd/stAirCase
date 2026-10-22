package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/b070nd/stAirCase/src/internal/history"
	"github.com/b070nd/stAirCase/src/internal/orchestrator"
	"github.com/b070nd/stAirCase/src/internal/persistence"
	"github.com/b070nd/stAirCase/src/internal/profile"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func profileWorkspace(t *testing.T) (ws string, projectID int64) {
	t.Helper()
	ws = t.TempDir()
	viper.Set("STAIRCASE_DIR", ws)
	t.Cleanup(func() { viper.Set("STAIRCASE_DIR", "") })
	db, err := persistence.InitDB(ws)
	require.NoError(t, err)
	defer db.Close()
	s := persistence.NewStore(db)
	v, err := s.CreateVendor("agency")
	require.NoError(t, err)
	p, err := s.CreateProject(v.ID, "shop", t.TempDir())
	require.NoError(t, err)
	return ws, p.ID
}

// TestProfile_set_and_show: a profile is changed field by field, a field named replaces it, --clear removes one, and what is set is
// what show says; it lives in the workspace.
func TestProfile_set_and_show(t *testing.T) {
	ws, id := profileWorkspace(t)
	var out bytes.Buffer
	require.NoError(t, showProfile(&out, ws, id))
	assert.Contains(t, out.String(), "has no profile")

	profileProtect, profileSensitive, profileScope, profileChecks, profileSandbox, profileClear = []string{"phpunit.xml"}, []string{"contracts/**"}, []string{"app/**"}, []string{"vendor/bin/phpunit"}, "", nil
	profileVisible = []string{"vendor"}
	out.Reset()
	require.NoError(t, setProfile(&out, ws, id, map[string]bool{"protect": true, "sensitive": true, "scope": true, "check": true, "visible": true}))
	for _, want := range []string{"protect", "phpunit.xml", "contracts/**", "app/**", "vendor/bin/phpunit", "visible", "vendor"} {
		assert.Contains(t, out.String(), want)
	}
	assert.FileExists(t, profile.Path(ws, id))

	profileScope, profileClear = []string{"app/code/**"}, []string{"protect", "checks"}
	out.Reset()
	require.NoError(t, setProfile(&out, ws, id, map[string]bool{"scope": true}))
	assert.Contains(t, out.String(), "app/code/**")
	assert.NotContains(t, out.String(), "phpunit")
	assert.Contains(t, out.String(), "contracts/**", "a field that was not named stays")

	profileClear = []string{"nonsense"}
	assert.ErrorContains(t, setProfile(&out, ws, id, map[string]bool{"clear": true}), "one of")
	profileClear = nil
	assert.ErrorContains(t, setProfile(&out, ws, id, map[string]bool{}), "nothing to set")
	profileScope = []string{"../outside"}
	assert.ErrorContains(t, setProfile(&out, ws, id, map[string]bool{"scope": true}), "inside the repository")
	assert.ErrorContains(t, showProfile(&out, ws, 99), "not found")
}

// TestApplyProfile_precedence: what the command line says wins, then the profile, then the default; a project's sensitive paths are
// always added; the digest of the profile goes into the terms.
func TestApplyProfile_precedence(t *testing.T) {
	ws, id := profileWorkspace(t)
	require.NoError(t, profile.Save(ws, id, profile.Profile{Checks: []string{"make test"}, Protect: []string{"Makefile.test"}, Sandbox: "required",
		Scope: []string{"src/**"}, Sensitive: []string{"contracts/**"}, Visible: []string{"vendor"}}))
	t.Cleanup(func() { explicitFlags = map[string]bool{} })

	var l launch
	explicitFlags = map[string]bool{}
	require.NoError(t, applyProfile(&l, ws, id))
	assert.Equal(t, []string{"make test"}, l.terms.Checks)
	assert.Equal(t, []string{"Makefile.test"}, l.terms.ProtectedPaths)
	assert.Equal(t, "required", l.terms.Sandbox)
	assert.Equal(t, []string{"src/**"}, l.allow)
	assert.Equal(t, []string{"vendor"}, l.terms.Visible)
	assert.Equal(t, []string{"contracts/**"}, l.terms.SensitivePaths)
	want, _, _ := profile.Load(ws, id)
	assert.Equal(t, want.Digest(), l.terms.ProfileSHA256)

	l = launch{}
	l.terms.Checks = []string{"go test ./..."}
	l.terms.Sandbox = "off"
	l.allow = []string{"docs/**"}
	l.terms.Visible = []string{"generated"}
	explicitFlags = map[string]bool{"check": true, "sandbox": true, "allow": true, "visible": true}
	require.NoError(t, applyProfile(&l, ws, id))
	assert.Equal(t, []string{"generated"}, l.terms.Visible, "a flag wins here too")
	assert.Equal(t, []string{"go test ./..."}, l.terms.Checks, "a flag on the command line wins")
	assert.Equal(t, "off", l.terms.Sandbox)
	assert.Equal(t, []string{"docs/**"}, l.allow)
	assert.Equal(t, []string{"Makefile.test"}, l.terms.ProtectedPaths, "what no flag named comes from the profile")
	assert.Equal(t, []string{"contracts/**"}, l.terms.SensitivePaths, "a flag cannot take away a project's sensitive paths")

	l = launch{}
	require.NoError(t, applyProfile(&l, ws, id+1))
	assert.Empty(t, l.terms.ProfileSHA256, "a project without a profile changes nothing")
}

// TestSession_runs_under_the_projects_profile: a session in a repository whose project has a profile starts under it: the checks run on
// the commit, the scope is the story's, the profile's digest is in the terms the run saved, and none of it is typed on the command line.
func TestSession_runs_under_the_projects_profile(t *testing.T) {
	repo, ws, git := sessionRepo(t)
	t.Cleanup(func() { explicitFlags = map[string]bool{}; cli.yes = false })
	require.NoError(t, claudeSession(nil, []string{"first"})) // makes the project
	db, err := persistence.InitDB(ws)
	require.NoError(t, err)
	projects, err := persistence.NewStore(db).ListAllProjects()
	require.NoError(t, err)
	require.Len(t, projects, 1)
	id := projects[0].ID
	require.NoError(t, db.Close())
	require.NoError(t, profile.Save(ws, id, profile.Profile{Checks: []string{"test -f HEALTH.md"}, Scope: []string{"HEALTH.md"}, Sensitive: []string{"contracts/**"},
		Instructions: "Keep the house header on every file."}))

	cli.yes = true // a task with checks and a scope is agreed to, and no terminal is here to ask
	require.NoError(t, claudeSession(nil, []string{"again"}))
	assert.Equal(t, "ok\n", git("show", "staircase/run-2:HEALTH.md"))
	assert.NoFileExists(t, repo+"/HEALTH.md", "the checkout is not touched")

	db, err = persistence.InitDB(ws)
	require.NoError(t, err)
	defer db.Close()
	s := persistence.NewStore(db)
	h, err := history.Read(s, 2)
	require.NoError(t, err)
	terms, err := orchestrator.SavedTerms(ws, 2, h)
	require.NoError(t, err)
	assert.Equal(t, []string{"test -f HEALTH.md"}, terms.Checks, "the profile's checks are the run's")
	assert.Equal(t, []string{"contracts/**"}, terms.SensitivePaths)
	assert.Equal(t, "Keep the house header on every file.", terms.Instructions, "the instructions are the run's, and a continuation keeps them")
	assert.Equal(t, 36, h.Bound.InstructionsBytes)
	assert.NotEmpty(t, h.Bound.InstructionsSHA256, "what the agent was told is bound on the run")
	var shown bytes.Buffer
	require.NoError(t, showBriefing(&shown, ws, 2))
	assert.Contains(t, shown.String(), "Keep the house header on every file.")
	assert.Less(t, strings.Index(shown.String(), "Keep the house header"), strings.Index(shown.String(), "again"), "the instructions come before the task")
	p, _, err := profile.Load(ws, id)
	require.NoError(t, err)
	assert.Equal(t, p.Digest(), terms.ProfileSHA256)
	logs, err := s.ListEventLogs(2)
	require.NoError(t, err)
	var checked bool
	for _, e := range logs {
		checked = checked || (e.EventType == "check_ran" && bytes.Contains([]byte(e.Payload), []byte(`"exit_code":0`)))
	}
	assert.True(t, checked, "the check ran on the commit")
	_, err = os.Stat(profile.Path(ws, id))
	assert.NoError(t, err)

	t1, err := orchestrator.SavedTerms(ws, 1, mustHistory(t, s, 1))
	require.NoError(t, err)
	assert.Empty(t, t1.ProfileSHA256, "the run before the profile existed has none")
}

func mustHistory(t *testing.T, s *persistence.Store, run int64) history.History {
	t.Helper()
	h, err := history.Read(s, run)
	require.NoError(t, err)
	return h
}

// TestSession_checks_see_the_projects_environment (spec: real use): the project's profile declares its ignored vendor/ directory as the
// environment; the check that needs it passes on the commit, in the macOS sandbox where there is one, the record says the check saw
// it, and verify says so; the checkout, vendor/ included, is untouched.
func TestSession_checks_see_the_projects_environment(t *testing.T) {
	repo, ws, git := sessionRepo(t)
	t.Cleanup(func() { explicitFlags = map[string]bool{}; cli.yes = false })
	require.NoError(t, claudeSession(nil, []string{"first"})) // makes the project
	db, err := persistence.InitDB(ws)
	require.NoError(t, err)
	projects, err := persistence.NewStore(db).ListAllProjects()
	require.NoError(t, err)
	id := projects[0].ID
	require.NoError(t, db.Close())

	require.NoError(t, os.WriteFile(filepath.Join(repo, ".gitignore"), []byte("vendor/\n"), 0o644))
	git("add", ".gitignore")
	git("commit", "-q", "-m", "ignore vendor")
	require.NoError(t, os.MkdirAll(filepath.Join(repo, "vendor"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "vendor", "autoload.php"), []byte("<?php\n"), 0o644))
	require.NoError(t, profile.Save(ws, id, profile.Profile{Checks: []string{"test -f vendor/autoload.php && test -f HEALTH.md"}, Visible: []string{"vendor"}}))

	cli.yes = true
	require.NoError(t, claudeSession(nil, []string{"again"}))
	assert.Equal(t, "ok\n", git("show", "staircase/run-2:HEALTH.md"))
	assert.FileExists(t, filepath.Join(repo, "vendor", "autoload.php"), "the user's vendor/ is untouched")
	assert.Equal(t, "", strings.TrimSpace(git("status", "--porcelain")), "the checkout is clean: the environment lives in a throwaway checkout")

	db, err = persistence.InitDB(ws)
	require.NoError(t, err)
	defer db.Close()
	logs, err := persistence.NewStore(db).ListEventLogs(2)
	require.NoError(t, err)
	var seen bool
	for _, e := range logs {
		if e.EventType == "check_ran" {
			assert.Contains(t, e.Payload, `"exit_code":0`, "the check that needs vendor/ passes")
			assert.Contains(t, e.Payload, `"environment":["vendor"]`)
			seen = true
		}
	}
	require.True(t, seen)
	verifyMinCAL, verifyKey, verifyFile = 0, "", ""
	out, err := captureStdout(t, func() error { return verifyHandler(nil, []string{"staircase/run-2"}) })
	require.NoError(t, err)
	assert.Contains(t, out, "with the declared environment vendor")
}

// TestProfile_instructions: the project's instructions are set from text or from a file (the profile keeps a copy), shown with their size
// and digest, cleared, and refused when too long; a briefing that does not exist is an error that says what to run.
func TestProfile_instructions(t *testing.T) {
	ws, id := profileWorkspace(t)
	t.Cleanup(func() { profileInstructions, profileInstructionsFile, profileClear = "", "", nil })
	var out bytes.Buffer
	profileInstructions = "Patches only. Never edit vendor/.\nProd is read-only."
	require.NoError(t, setProfile(&out, ws, id, map[string]bool{"instructions": true}))
	assert.Contains(t, out.String(), "instructions")
	assert.Contains(t, out.String(), "Patches only. Never edit vendor/.")
	assert.NotContains(t, out.String(), "Prod is read-only", "show prints the first line, not the text")
	assert.Contains(t, out.String(), "52 bytes")

	file := filepath.Join(t.TempDir(), "rules.md")
	require.NoError(t, os.WriteFile(file, []byte("From a file.\n"), 0o644))
	profileInstructions, profileInstructionsFile = "", file
	require.NoError(t, setProfile(&out, ws, id, map[string]bool{"instructions-file": true}))
	got, _, err := profile.Load(ws, id)
	require.NoError(t, err)
	assert.Equal(t, "From a file.", got.Instructions)
	require.NoError(t, os.Remove(file))
	_, _, err = profile.Load(ws, id)
	require.NoError(t, err, "the profile keeps a copy: the file may go")

	assert.ErrorContains(t, setProfile(&out, ws, id, map[string]bool{"instructions": true, "instructions-file": true}), "once")
	profileInstructionsFile = file // gone now
	assert.ErrorContains(t, setProfile(&out, ws, id, map[string]bool{"instructions-file": true}), "--instructions-file")
	profileInstructions, profileInstructionsFile = strings.Repeat("x", profile.MaxInstructions+1), ""
	assert.ErrorContains(t, setProfile(&out, ws, id, map[string]bool{"instructions": true}), "at most")

	profileInstructions, profileClear = "", []string{"instructions"}
	require.NoError(t, setProfile(&out, ws, id, map[string]bool{"clear": true}))
	_, found, _ := profile.Load(ws, id)
	assert.False(t, found, "with nothing left the profile is gone")

	assert.ErrorContains(t, showBriefing(&out, ws, 5), "is not compiled")
}
