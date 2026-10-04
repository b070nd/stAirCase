package main

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestBudgetCap_must_be_a_real_amount: a negative or not-a-number cap would be
// stored and reported as set while capping nothing.
func TestBudgetCap_must_be_a_real_amount(t *testing.T) {
	for _, ok := range []float64{0, 0.5, 25} {
		assert.NoError(t, checkBudgetCap(ok), ok)
	}
	for _, bad := range []float64{-1, -0.01, math.NaN(), math.Inf(1), math.Inf(-1)} {
		assert.Error(t, checkBudgetCap(bad), bad)
	}
}
