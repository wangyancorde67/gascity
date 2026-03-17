package bundle

import (
	"testing"
)

// TestSchemaVersionCompat verifies that the runtime accepts bundles at the
// exact current schema version and rejects all others.
func TestSchemaVersionCompat(t *testing.T) {
	t.Run("AcceptsCurrent", func(t *testing.T) {
		if !CanAccept(SchemaVersion) {
			t.Errorf("CanAccept(N=%d) = false, must accept current version", SchemaVersion)
		}
	})

	t.Run("RejectsNext", func(t *testing.T) {
		next := SchemaVersion + 1
		if CanAccept(next) {
			t.Errorf("CanAccept(N+1=%d) = true, must reject future version", next)
		}
	})

	t.Run("RejectsZero", func(t *testing.T) {
		if CanAccept(0) {
			t.Error("CanAccept(0) = true, must reject version 0")
		}
	})
}
