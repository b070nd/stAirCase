package changeset

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var now = time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)

func TestID_is_a_plain_name(t *testing.T) {
	for _, ok := range []string{"checkout-v2", "a", "sc-128.1", "release_7"} {
		assert.NoError(t, ValidID(ok), ok)
	}
	for _, bad := range []string{"", "-x", "../x", "a/b", "A", "has space", "x" + string(make([]byte, 80))} {
		assert.Error(t, ValidID(bad), "%q", bad)
	}
}

func TestSet_is_kept_in_the_workspace_and_found_again(t *testing.T) {
	ws := t.TempDir()
	_, err := Load(ws, "nope")
	require.ErrorContains(t, err, `no change set "nope"`)
	require.ErrorContains(t, err, "staircase changeset create nope")

	s, err := Create(ws, "checkout", now)
	require.NoError(t, err)
	assert.Equal(t, "checkout", s.ID)
	_, err = Create(ws, "checkout", now)
	require.ErrorContains(t, err, "already exists", "a set is made once: its id names the work")

	_, err = s.AddRun(1, 10)
	require.NoError(t, err)
	require.NoError(t, s.Declare(Contract{Project: 1, Path: "api/openapi.yaml"}))
	require.NoError(t, s.Save(ws))
	got, err := Load(ws, "checkout")
	require.NoError(t, err)
	assert.Equal(t, s, got)
	ids, err := List(ws)
	require.NoError(t, err)
	assert.Equal(t, []string{"checkout"}, ids)
}

func TestSet_a_project_has_one_run_and_a_new_one_replaces_it(t *testing.T) {
	s := Set{ID: "x"}
	add := func(project, run int64) int64 {
		t.Helper()
		replaced, err := s.AddRun(project, run)
		require.NoError(t, err)
		return replaced
	}
	assert.Zero(t, add(1, 10))
	assert.Zero(t, add(2, 20))
	assert.EqualValues(t, 10, add(1, 11), "the run it replaces is named")
	assert.Equal(t, []Member{{Project: 1, Run: 11}, {Project: 2, Run: 20}}, s.Members)
	assert.Zero(t, add(1, 11), "adding what is there changes nothing")
	assert.Len(t, s.Members, 2)
}

func TestSet_contracts_are_literal_paths_inside_the_repository(t *testing.T) {
	s := Set{ID: "x"}
	for _, bad := range []string{"", "/etc/passwd", "../x", "a/*.yaml", ".git/config"} {
		assert.Error(t, s.Declare(Contract{Project: 1, Path: bad}), "%q", bad)
	}
	require.NoError(t, s.Declare(Contract{Project: 1, Path: "./api//openapi.yaml"}))
	require.NoError(t, s.Declare(Contract{Project: 1, Path: "api/openapi.yaml"}))
	assert.Equal(t, []Contract{{Project: 1, Path: "api/openapi.yaml"}}, s.Contracts)
}

func TestSet_acknowledgement_needs_a_declared_contract_and_another_member(t *testing.T) {
	s := Set{ID: "x"}
	c := Contract{Project: 1, Path: "api/openapi.yaml"}
	_, _ = s.AddRun(1, 10)
	_, _ = s.AddRun(2, 20)
	assert.ErrorContains(t, s.Acknowledge(c, 2, 10, "b1", "me@x", now), "not a contract of this set")
	require.NoError(t, s.Declare(c))
	assert.ErrorContains(t, s.Acknowledge(c, 1, 10, "b1", "me@x", now), "cannot acknowledge its own change")
	assert.ErrorContains(t, s.Acknowledge(c, 3, 10, "b1", "me@x", now), "has no run in this set")
	require.NoError(t, s.Acknowledge(c, 2, 10, "b1", "me@x", now))
	assert.True(t, s.Acknowledged(c, 2, "b1"))
	assert.False(t, s.Acknowledged(c, 2, "b2"), "an acknowledgement is of the content it saw: a later change needs a new one")
	require.NoError(t, s.Acknowledge(c, 2, 10, "b1", "me@x", now), "again changes nothing")
	assert.Len(t, s.Acks, 1)
}

func TestLoad_refuses_a_file_it_cannot_read_or_that_names_another_set(t *testing.T) {
	ws := t.TempDir()
	s, err := Create(ws, "a", now)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(Path(ws, "a"), []byte(`{"id":"a","surprise":1}`), 0o600))
	_, err = Load(ws, "a")
	require.Error(t, err, "a misspelt field would silently drop an acknowledgement")
	require.NoError(t, os.WriteFile(Path(ws, "a"), []byte(`{"id":"b"}`), 0o600))
	_, err = Load(ws, "a")
	require.ErrorContains(t, err, `names "b"`)
	_ = s
}
