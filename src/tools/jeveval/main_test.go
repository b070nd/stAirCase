package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/b070nd/stAirCase/src/internal/signal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// perfect answers every case as labelled, except that it misses exfil-ssh.
type perfect struct{ cases map[string]evalCase }

func (p perfect) Evaluate(_ context.Context, state any, _ map[string]signal.Question) (signal.Result, error) {
	k := p.cases[key(state)]
	risky := b2f(k.Risky)
	if k.ID == "exfil-ssh" {
		risky = 0.2
	}
	return signal.Result{Cost: 0.00001, Answers: map[string]signal.Answer{
		"risky": {Probability: risky}, "serves_story": {Probability: b2f(k.Serves)}, "kind": {Choice: k.Kind}}}, nil
}

// TestRun_reports_accuracy_and_misses: the built-in cases load, and the
// report counts accuracy and names the risky change a model missed.
func TestRun_reports_accuracy_and_misses(t *testing.T) {
	var cases []evalCase
	require.NoError(t, json.Unmarshal(defaultCases, &cases))
	byKey := map[string]evalCase{}
	for _, k := range cases {
		byKey[key(map[string]any{"stories": k.Story, "change": []map[string]any{{"path": k.Path, "before": k.Before, "after": k.After}}})] = k
	}
	var out bytes.Buffer
	require.NoError(t, run(context.Background(), perfect{byKey}, "fake", cases, &out))
	n := len(cases)
	assert.Contains(t, out.String(), "| risky | "+itoa(n-1)+"/"+itoa(n))
	assert.Contains(t, out.String(), "| kind | "+itoa(n)+"/"+itoa(n))
	assert.Contains(t, out.String(), "missed (would not reach a person): exfil-ssh (0.20)")
	assert.True(t, strings.Contains(out.String(), "flagged (a person asked needlessly): none"), out.String())
}

func itoa(i int) string { b, _ := json.Marshal(i); return string(b) }

func key(state any) string { b, _ := json.Marshal(state); return string(b) }
