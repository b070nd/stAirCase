package chain_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/b070nd/stAirCase/src/internal/chain"
	"github.com/b070nd/stAirCase/src/internal/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestVectors: the conformance vectors of docs/spec/audit-chain-vectors.json (also checked by audit_chain_vectors.py, which shares
// no code with this package) are decided as the file says.
func TestVectors(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "docs", "spec", "audit-chain-vectors.json"))
	require.NoError(t, err)
	var vectors map[string]struct {
		Valid   bool                 `json:"valid"`
		Entries []domain.RunEventLog `json:"entries"`
	}
	require.NoError(t, json.Unmarshal(raw, &vectors))
	require.NotEmpty(t, vectors)
	for name, v := range vectors {
		t.Run(name, func(t *testing.T) {
			err := chain.Verify(v.Entries)
			if v.Valid {
				assert.NoError(t, err)
			} else {
				assert.Error(t, err)
			}
		})
	}
}

type fixture struct {
	Entries []domain.RunEventLog `json:"entries"`
}

func readFixture(t *testing.T) []domain.RunEventLog {
	raw, err := os.ReadFile(filepath.Join("testdata", "fixture_chain.json"))
	require.NoError(t, err)
	var f fixture
	require.NoError(t, json.Unmarshal(raw, &f))
	require.NotEmpty(t, f.Entries)
	return f.Entries
}

// TestFixture_chain_verifies (ADR 0014, section 2): the fixture is a chain the code before this change wrote, one entry for every shape
// of payload the runs write. Its hashes were computed by the functions as they were; the chain package reproduces every one and
// verifies the whole, so moving the rule changed no hash. Altering one byte of any entry is named.
func TestFixture_chain_verifies(t *testing.T) {
	entries := readFixture(t)
	require.NoError(t, chain.Verify(entries))
	prev := ""
	for _, e := range entries {
		assert.Equal(t, chain.HashV2(e.EventType, e.Payload, prev, e.GitCommitHash), e.EventHash, "entry %d (%s)", e.ID, e.EventType)
		prev = e.EventHash
	}
	for i := range entries {
		tampered := append([]domain.RunEventLog(nil), entries...)
		tampered[i].Payload += " "
		err := chain.Verify(tampered)
		require.Error(t, err, "a byte added to entry %d", i+1)
		assert.Contains(t, err.Error(), tampered[i].EventType)
	}
}

// TestFixture_has_no_local_trace: the fixture is public test data, taken from runs on a developer's machine.
func TestFixture_has_no_local_trace(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "fixture_chain.json"))
	require.NoError(t, err)
	for _, bad := range []string{"/Users/", "/var/folders", "/private/", "AKIA", "-----BEGIN"} {
		assert.NotContains(t, string(raw), bad)
	}
}

func TestCheckSize(t *testing.T) {
	assert.NoError(t, chain.CheckSize("e", strings.Repeat("a", chain.MaxPayload)))
	err := chain.CheckSize("awaiting_review", strings.Repeat("a", chain.MaxPayload+1))
	require.ErrorIs(t, err, chain.ErrPayloadTooLarge)
	assert.Contains(t, err.Error(), "awaiting_review")
	assert.Contains(t, err.Error(), "1048577")
}

func TestExpected_refuses_a_version_it_does_not_know(t *testing.T) {
	_, err := chain.Expected(domain.RunEventLog{ID: 7, HashVersion: 9}, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "chain version 9")
}

// TestCheck: every entry is judged against the hash stored on the one before it, so a broken entry is named and the entries after
// it are still judged; Verify names the first.
func TestCheck_judges_each_entry_and_verify_names_the_first(t *testing.T) {
	entries := readFixture(t)
	for _, err := range chain.Check(entries) {
		require.NoError(t, err)
	}
	tampered := append([]domain.RunEventLog(nil), entries...)
	tampered[3].Payload += " "
	tampered[9].Payload += " "
	got := chain.Check(tampered)
	require.Len(t, got, len(entries))
	for i, err := range got {
		if i == 3 || i == 9 {
			require.Error(t, err, "entry %d", i+1)
		} else {
			assert.NoError(t, err, "entry %d is judged on its own, against the hash stored before it", i+1)
		}
	}
	err := chain.Verify(tampered)
	require.Error(t, err)
	assert.Equal(t, got[3].Error(), err.Error(), "Verify names the first")
}
