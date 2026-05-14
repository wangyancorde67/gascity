package gas_test

import (
	"testing"

	"github.com/gastownhall/gascity/internal/gas"
)

func TestAlertThresholds_Validate(t *testing.T) {
	tests := []struct {
		name    string
		t       gas.AlertThresholds
		wantErr bool
	}{
		{"valid", gas.AlertThresholds{Warning: 50, Critical: 100}, false},
		{"zero warning", gas.AlertThresholds{Warning: 0, Critical: 100}, true},
		{"zero critical", gas.AlertThresholds{Warning: 50, Critical: 0}, true},
		{"warning equals critical", gas.AlertThresholds{Warning: 100, Critical: 100}, true},
		{"warning exceeds critical", gas.AlertThresholds{Warning: 150, Critical: 100}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.t.Validate()
			if (err != nil) != tc.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

func TestEvaluateAlert(t *testing.T) {
	thresholds := gas.AlertThresholds{Warning: 50, Critical: 100}

	tests := []struct {
		name      string
		priceGwei uint64
		want      gas.AlertLevel
		wantErr   bool
	}{
		{"below warning", 30, gas.AlertNone, false},
		{"at warning", 50, gas.AlertWarning, false},
		{"between warning and critical", 75, gas.AlertWarning, false},
		{"at critical", 100, gas.AlertCritical, false},
		{"above critical", 200, gas.AlertCritical, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := gas.EvaluateAlert(tc.priceGwei, thresholds)
			if (err != nil) != tc.wantErr {
				t.Fatalf("EvaluateAlert() error = %v, wantErr %v", err, tc.wantErr)
			}
			if got != tc.want {
				t.Errorf("EvaluateAlert() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestEvaluateAlert_InvalidThresholds(t *testing.T) {
	_, err := gas.EvaluateAlert(50, gas.AlertThresholds{Warning: 0, Critical: 100})
	if err == nil {
		t.Error("expected error for invalid thresholds, got nil")
	}
}

func TestAlertLevel_String(t *testing.T) {
	cases := []struct {
		level gas.AlertLevel
		want  string
	}{
		{gas.AlertNone, "none"},
		{gas.AlertWarning, "warning"},
		{gas.AlertCritical, "critical"},
		{gas.AlertLevel(99), "unknown"},
	}
	for _, tc := range cases {
		if got := tc.level.String(); got != tc.want {
			t.Errorf("AlertLevel(%d).String() = %q, want %q", tc.level, got, tc.want)
		}
	}
}
