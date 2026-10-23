package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/b070nd/stAirCase/src/internal/certificate"
	"github.com/b070nd/stAirCase/src/internal/changeset"
	"github.com/b070nd/stAirCase/src/internal/persistence"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// changesetWorkspace is two repositories, "shop" and "api", one workspace, and a run in each under the change set "checkout"; each run
// makes HEALTH.md, which stands for the contract file here. It returns the run ids and what is needed to run again.
func changesetWorkspace(t *testing.T) (ws string, shopRun, apiRun int64, again func(dir, task string)) {
	t.Helper()
	_, ws, _ = sessionRepo(t)
	t.Cleanup(func() { explicitFlags = map[string]bool{}; cli.yes = false })
	shop, err := os.Getwd()
	require.NoError(t, err)

	api := filepath.Join(t.TempDir(), "api")
	require.NoError(t, os.MkdirAll(api, 0o755))
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"config", "user.email", "t@t"}, {"config", "user.name", "T"}} {
		out, err := exec.Command("git", append([]string{"-C", api}, args...)...).CombinedOutput()
		require.NoError(t, err, "%s", out)
	}
	require.NoError(t, os.WriteFile(filepath.Join(api, "README.md"), []byte("api\n"), 0o644))
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-q", "-m", "init"}} {
		out, err := exec.Command("git", append([]string{"-C", api}, args...)...).CombinedOutput()
		require.NoError(t, err, "%s", out)
	}

	_, err = changeset.Create(ws, "checkout", time.Now())
	require.NoError(t, err)
	again = func(dir, task string) {
		t.Helper()
		t.Chdir(dir)
		cli.yes = true
		require.NoError(t, claudeSession(nil, []string{task}))
	}
	cli.terms.ChangeSet = "checkout"
	again(shop, "shop side")
	again(api, "api side")
	return ws, 1, 2, again
}

func projectIDs(t *testing.T, ws string) map[string]int64 {
	t.Helper()
	db, err := persistence.InitDB(ws)
	require.NoError(t, err)
	defer db.Close()
	projects, err := persistence.NewStore(db).ListAllProjects()
	require.NoError(t, err)
	ids := map[string]int64{}
	for _, p := range projects {
		ids[p.Name] = p.ID
	}
	return ids
}

func changesetDo(t *testing.T, f func(w *bytes.Buffer) error) (string, error) {
	t.Helper()
	var out bytes.Buffer
	err := f(&out)
	return out.String(), err
}

// TestChangeSet_two_repositories_one_piece_of_work (spec: real use, change sets): each run stays one repository and names its set on its
// certificate; the set holds both; a contract that one changed fails the set, naming both, until the other acknowledges that content;
// and a member that is missing, undelivered or whose certificate was changed fails it, naming the member.
func TestChangeSet_two_repositories_one_piece_of_work(t *testing.T) {
	ws, shopRun, apiRun, _ := changesetWorkspace(t)
	ids := projectIDs(t, ws)
	require.Len(t, ids, 2)

	// the certificate of each run names the set, and nothing else about a run changed
	for _, run := range []int64{shopRun, apiRun} {
		raw, err := os.ReadFile(certificatePath(ws, run))
		require.NoError(t, err)
		var env certificate.Envelope
		require.NoError(t, json.Unmarshal(raw, &env))
		payload, err := base64.StdEncoding.DecodeString(env.Payload)
		require.NoError(t, err)
		assert.Contains(t, string(payload), `"changeSet":"checkout"`)
	}

	_, err := changesetDo(t, func(w *bytes.Buffer) error { return changesetAdd(w, "checkout", []int64{shopRun, apiRun}) })
	require.NoError(t, err)
	out, err := changesetDo(t, func(w *bytes.Buffer) error { return changesetShow(w, "checkout") })
	require.NoError(t, err)
	assert.Contains(t, out, "shop: run #1")
	assert.Contains(t, out, "api: run #2")

	out, err = changesetDo(t, func(w *bytes.Buffer) error { return changesetVerify(w, "checkout", 0) })
	require.NoError(t, err, out)
	assert.Contains(t, out, "holds")

	// a contract that the shop's run changed, and the api has not acknowledged
	out, err = changesetDo(t, func(w *bytes.Buffer) error { return changesetContract(w, "checkout", "shop:HEALTH.md") })
	require.NoError(t, err)
	assert.Contains(t, out, "does not always need a person yet", "the contract is not yet person-only in the profile")
	out, err = changesetDo(t, func(w *bytes.Buffer) error { return changesetVerify(w, "checkout", 0) })
	require.Error(t, err)
	assert.Contains(t, out, "shop")
	assert.Contains(t, out, "HEALTH.md")
	assert.Contains(t, out, "api")
	assert.Contains(t, out, "has not acknowledged")

	// the api's own file by the same name is not the shop's contract: nothing to acknowledge there
	_, err = changesetDo(t, func(w *bytes.Buffer) error { return changesetAck(w, "checkout", "api:HEALTH.md", "shop") })
	require.Error(t, err, "it is not a contract of the set")

	// the shop cannot accept its own change, and nobody outside the set can accept it
	_, err = changesetDo(t, func(w *bytes.Buffer) error { return changesetAck(w, "checkout", "shop:HEALTH.md", "shop") })
	require.ErrorContains(t, err, "own change")
	_, err = changesetDo(t, func(w *bytes.Buffer) error { return changesetAck(w, "checkout", "shop:HEALTH.md", "nobody") })
	require.Error(t, err)

	out, err = changesetDo(t, func(w *bytes.Buffer) error { return changesetAck(w, "checkout", "shop:HEALTH.md", "api") })
	require.NoError(t, err)
	assert.Contains(t, out, "api accepts run #1")
	out, err = changesetDo(t, func(w *bytes.Buffer) error { return changesetVerify(w, "checkout", 0) })
	require.NoError(t, err, out)
	assert.Contains(t, out, "changed the contract HEALTH.md")

	// the level asked is held to: a run of this kind is below CAL 4
	out, err = changesetDo(t, func(w *bytes.Buffer) error { return changesetVerify(w, "checkout", 4) })
	require.Error(t, err)
	assert.Contains(t, out, "below the required 4")

	// a certificate that was changed fails its member, by name
	path := certificatePath(ws, apiRun)
	good, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, bytes.Replace(good, []byte(`"payload": "`), []byte(`"payload": "AAAA`), 1), 0o600))
	out, err = changesetDo(t, func(w *bytes.Buffer) error { return changesetVerify(w, "checkout", 0) })
	require.Error(t, err)
	assert.Contains(t, out, "api (#"+strconv.FormatInt(ids["api"], 10)+") run #2: FAILED")
	assert.Contains(t, out, "does not verify")
	require.NoError(t, os.WriteFile(path, good, 0o600))

	// a run that is not delivered fails its member, by name
	db, err := persistence.InitDB(ws)
	require.NoError(t, err)
	store := persistence.NewStore(db)
	cases, err := store.ListCasesByProject(ids["api"])
	require.NoError(t, err)
	topo, err := store.CreateSwarmTopology(ids["api"], "sup", "memory", "langgraph") // a run needs a topology to exist
	require.NoError(t, err)
	stray, err := store.CreateRun(cases[0].ID, topo.Version, "staircase/run-stray")
	require.NoError(t, err)
	require.NoError(t, db.Close())
	_, err = changesetDo(t, func(w *bytes.Buffer) error { return changesetAdd(w, "checkout", []int64{stray.ID}) })
	require.NoError(t, err)
	out, err = changesetDo(t, func(w *bytes.Buffer) error { return changesetVerify(w, "checkout", 0) })
	require.Error(t, err)
	assert.Contains(t, out, "api")
	assert.Contains(t, out, "is not delivered")
}

// TestChangeSet_a_run_that_does_not_name_the_set_fails_it: a run made without --change-set can be added, and the set does not hold: its
// certificate names no set, so nobody holding it could find the others.
func TestChangeSet_a_run_that_does_not_name_the_set_fails_it(t *testing.T) {
	ws, _, _, again := changesetWorkspace(t)
	last, err := os.Getwd() // the api's: the helper ended there
	require.NoError(t, err)
	cli.terms.ChangeSet = ""
	again(last, "a run of its own")
	_, err = changesetDo(t, func(w *bytes.Buffer) error { return changesetAdd(w, "checkout", []int64{3}) })
	require.NoError(t, err)
	out, err := changesetDo(t, func(w *bytes.Buffer) error { return changesetVerify(w, "checkout", 0) })
	require.Error(t, err)
	assert.Contains(t, out, "names no change set")
	_ = ws
}

// TestChangeSet_a_run_names_only_a_set_that_exists: the id is written on a certificate, so a misspelt one is refused before the run.
func TestChangeSet_a_run_names_only_a_set_that_exists(t *testing.T) {
	repo, _, _ := sessionRepo(t)
	t.Cleanup(func() { explicitFlags = map[string]bool{}; cli.yes = false })
	cli.terms.ChangeSet = "chekout"
	err := claudeSession(nil, []string{"x"})
	require.ErrorContains(t, err, `no change set "chekout"`)
	cli.terms.ChangeSet = "Not A Name"
	require.Error(t, claudeSession(nil, []string{"x"}))
	assert.NoFileExists(t, filepath.Join(repo, "HEALTH.md"))
}
