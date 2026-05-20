#!/usr/bin/env bash
# build.sh — build the tiny-go fixture repository tarball.
#
# Creates a minimal Go repository (200 LoC, 5 files) with a deterministic
# git history.  Reproducible given the same SOURCE_DATE_EPOCH.
#
# Usage: SOURCE_DATE_EPOCH=1700000000 bash build.sh
# Output: tiny-go.tar.gz in the same directory as this script.
#
# Requirements: git, tar, gzip (all standard on macOS and Linux)
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
OUT="$SCRIPT_DIR/tiny-go.tar.gz"

# Deterministic commit timestamp (falls back to a fixed date if unset).
EPOCH="${SOURCE_DATE_EPOCH:-1700000000}"
COMMIT_DATE="$(date -u -r "$EPOCH" '+%Y-%m-%dT%H:%M:%S +0000' 2>/dev/null || \
               date -u -d "@$EPOCH" '+%Y-%m-%dT%H:%M:%S +0000')"

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

REPO="$WORK/tiny-go"
mkdir -p "$REPO"

# ── git init ──────────────────────────────────────────────────────────────────
git -C "$REPO" init -b main -q
git -C "$REPO" config user.email "fixture@staircase.test"
git -C "$REPO" config user.name  "Fixture Builder"

# ── go.mod ────────────────────────────────────────────────────────────────────
cat > "$REPO/go.mod" << 'GOMOD'
module example.com/tiny

go 1.22
GOMOD

# ── calc/calc.go ──────────────────────────────────────────────────────────────
mkdir -p "$REPO/calc"
cat > "$REPO/calc/calc.go" << 'CALCGO'
// Package calc provides simple arithmetic for stAirCase fixture testing.
package calc

import "errors"

// ErrDivByZero is returned when the divisor is zero.
var ErrDivByZero = errors.New("division by zero")

// Add returns a + b.
func Add(a, b int) int { return a + b }

// Sub returns a - b.
func Sub(a, b int) int { return a - b }

// Mul returns a * b.
func Mul(a, b int) int { return a * b }

// Div returns a / b.  Returns ErrDivByZero when b == 0.
func Div(a, b int) (int, error) {
	if b == 0 {
		return 0, ErrDivByZero
	}
	return a / b, nil
}

// Abs returns the absolute value of n.
func Abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// Max returns the larger of a and b.
func Max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// Min returns the smaller of a and b.
func Min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// Clamp returns n clamped to [lo, hi].
func Clamp(n, lo, hi int) int {
	return Max(lo, Min(hi, n))
}

// Sum returns the sum of all values.
func Sum(vals ...int) int {
	total := 0
	for _, v := range vals {
		total += v
	}
	return total
}
CALCGO

# ── calc/calc_test.go ─────────────────────────────────────────────────────────
cat > "$REPO/calc/calc_test.go" << 'CALCTEST'
package calc_test

import (
	"testing"

	"example.com/tiny/calc"
)

func TestAdd(t *testing.T)       { assertEqual(t, 5, calc.Add(2, 3)) }
func TestSub(t *testing.T)       { assertEqual(t, 1, calc.Sub(3, 2)) }
func TestMul(t *testing.T)       { assertEqual(t, 6, calc.Mul(2, 3)) }
func TestAbs_positive(t *testing.T) { assertEqual(t, 3, calc.Abs(3)) }
func TestAbs_negative(t *testing.T) { assertEqual(t, 3, calc.Abs(-3)) }
func TestMax(t *testing.T)       { assertEqual(t, 5, calc.Max(3, 5)) }
func TestMin(t *testing.T)       { assertEqual(t, 3, calc.Min(3, 5)) }
func TestClamp_in(t *testing.T)  { assertEqual(t, 4, calc.Clamp(4, 1, 9)) }
func TestClamp_lo(t *testing.T)  { assertEqual(t, 1, calc.Clamp(0, 1, 9)) }
func TestClamp_hi(t *testing.T)  { assertEqual(t, 9, calc.Clamp(99, 1, 9)) }
func TestSum(t *testing.T)       { assertEqual(t, 10, calc.Sum(1, 2, 3, 4)) }

func TestDiv_normal(t *testing.T) {
	got, err := calc.Div(10, 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertEqual(t, 5, got)
}

func TestDiv_by_zero(t *testing.T) {
	_, err := calc.Div(1, 0)
	if err != calc.ErrDivByZero {
		t.Fatalf("want ErrDivByZero, got %v", err)
	}
}

func assertEqual(t *testing.T, want, got int) {
	t.Helper()
	if want != got {
		t.Fatalf("want %d, got %d", want, got)
	}
}
CALCTEST

# ── main.go ───────────────────────────────────────────────────────────────────
cat > "$REPO/main.go" << 'MAINGO'
// Command tiny is a minimal CLI that demonstrates the calc package.
// It is intentionally simple so stAirCase agents have a small surface to edit.
package main

import (
	"fmt"
	"os"
	"strconv"

	"example.com/tiny/calc"
)

func main() {
	if len(os.Args) != 4 {
		fmt.Fprintln(os.Stderr, "usage: tiny <op> <a> <b>")
		fmt.Fprintln(os.Stderr, "  op: add sub mul div")
		os.Exit(1)
	}
	op := os.Args[1]
	a, err := strconv.Atoi(os.Args[2])
	if err != nil {
		fmt.Fprintf(os.Stderr, "bad a: %v\n", err)
		os.Exit(1)
	}
	b, err := strconv.Atoi(os.Args[3])
	if err != nil {
		fmt.Fprintf(os.Stderr, "bad b: %v\n", err)
		os.Exit(1)
	}
	switch op {
	case "add":
		fmt.Println(calc.Add(a, b))
	case "sub":
		fmt.Println(calc.Sub(a, b))
	case "mul":
		fmt.Println(calc.Mul(a, b))
	case "div":
		result, err := calc.Div(a, b)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println(result)
	default:
		fmt.Fprintf(os.Stderr, "unknown op: %s\n", op)
		os.Exit(1)
	}
}
MAINGO

# ── README.md ────────────────────────────────────────────────────────────────
cat > "$REPO/README.md" << 'README'
# tiny-go

A minimal Go repository used as a stAirCase test fixture.

## Usage

```sh
go run . add 3 4   # → 7
go run . div 10 0  # → division by zero (exit 1)
```

## Tests

```sh
go test ./...
```
README

# ── commit ────────────────────────────────────────────────────────────────────
git -C "$REPO" add -A
GIT_COMMITTER_DATE="$COMMIT_DATE" \
GIT_AUTHOR_DATE="$COMMIT_DATE" \
  git -C "$REPO" commit -q -m "init: minimal Go calculator"

# ── archive (include .git so tests can use git operations on the fixture) ─────
# We archive the directory contents (not the dir name itself) so extracting
# tiny-go.tar.gz into a tmpdir gives the repo directly.
tar czf "$OUT" -C "$REPO" .

echo "Built: $OUT"
echo "SHA256: $(shasum -a 256 "$OUT" | awk '{print $1}')"
