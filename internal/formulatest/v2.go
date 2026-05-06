// Package formulatest contains helpers for tests that exercise formula behavior
// from outside the formula package.
package formulatest

import (
	"sync"
	"testing"

	"github.com/gastownhall/gascity/internal/formula"
)

var (
	v2Mu          sync.Mutex
	v2EnableCount int
	v2SavedValue  bool
)

// EnableV2ForTest enables graph.v2 formula compilation for the duration of the
// test, restoring the previous value after the last concurrent enable cleanup.
func EnableV2ForTest(tb testing.TB) {
	tb.Helper()
	v2Mu.Lock()
	if v2EnableCount == 0 {
		v2SavedValue = formula.IsFormulaV2Enabled()
	}
	v2EnableCount++
	formula.SetFormulaV2Enabled(true)
	v2Mu.Unlock()

	tb.Cleanup(func() {
		v2Mu.Lock()
		defer v2Mu.Unlock()
		v2EnableCount--
		if v2EnableCount == 0 {
			formula.SetFormulaV2Enabled(v2SavedValue)
		}
	})
}
