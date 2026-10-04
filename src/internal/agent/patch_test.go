package agent

import (
	"testing"

	"github.com/b070nd/stAirCase/src/internal/domain"
	"github.com/b070nd/stAirCase/src/internal/orchestrator"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestParsePatch: Codex's apply_patch text becomes the edits staircase
// decides - a new file's whole content, one search/replace per hunk (old
// lines found, new lines put in their place), a deletion.
func TestParsePatch(t *testing.T) {
	edits, err := parsePatch(`*** Begin Patch
*** Add File: docs/HELLO.md
+hello
+world
*** Update File: src/app.go
@@ func main() {
 	fmt.Println("a")
-	fmt.Println("b")
+	fmt.Println("B")
+	fmt.Println("C")
 	fmt.Println("d")
@@
-old tail
+new tail
*** Delete File: OLD.md
*** End Patch
`)
	require.NoError(t, err)
	assert.Equal(t, []domain.ProposedEdit{
		{File: "docs/HELLO.md", SearchBlock: orchestrator.MarkerNewFile, ReplaceBlock: "hello\nworld\n"},
		{File: "src/app.go", SearchBlock: "\tfmt.Println(\"a\")\n\tfmt.Println(\"b\")\n\tfmt.Println(\"d\")\n",
			ReplaceBlock: "\tfmt.Println(\"a\")\n\tfmt.Println(\"B\")\n\tfmt.Println(\"C\")\n\tfmt.Println(\"d\")\n"},
		{File: "src/app.go", SearchBlock: "old tail\n", ReplaceBlock: "new tail\n"},
		{File: "OLD.md", SearchBlock: orchestrator.MarkerDeleteFile},
	}, edits)
}

// TestParsePatch_refuses what it cannot decide exactly.
func TestParsePatch_refuses(t *testing.T) {
	for name, patch := range map[string]string{
		"no envelope":      "*** Add File: a\n+x\n",
		"move":             "*** Begin Patch\n*** Update File: a\n*** Move to: b\n@@\n-x\n+y\n*** End Patch\n",
		"stray line":       "*** Begin Patch\n*** Update File: a\n@@\n?x\n*** End Patch\n",
		"empty update":     "*** Begin Patch\n*** Update File: a\n*** End Patch\n",
		"nothing to find":  "*** Begin Patch\n*** Update File: a\n@@\n+only added\n*** End Patch\n",
		"unknown section":  "*** Begin Patch\n*** Rename File: a\n*** End Patch\n",
		"no file sections": "*** Begin Patch\n*** End Patch\n",
	} {
		_, err := parsePatch(patch)
		assert.Error(t, err, name)
	}
}

// TestParsePatch_an_empty_new_file_is_empty: a file added with no lines is
// zero bytes, not one newline: the bytes approved are the bytes Codex writes.
func TestParsePatch_an_empty_new_file_is_empty(t *testing.T) {
	edits, err := parsePatch("*** Begin Patch\n*** Add File: pkg/.keep\n*** Add File: b.txt\n+\n*** End Patch\n")
	require.NoError(t, err)
	assert.Equal(t, []domain.ProposedEdit{
		{File: "pkg/.keep", SearchBlock: orchestrator.MarkerNewFile, ReplaceBlock: ""},
		{File: "b.txt", SearchBlock: orchestrator.MarkerNewFile, ReplaceBlock: "\n"},
	}, edits)
}
