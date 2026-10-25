package server

import "testing"

func TestNormalizeCloudState(t *testing.T) {
	for raw, want := range map[string]string{
		"running": "running", "Running": "running", "RUNNING": "running", " RUNNING ": "running",
		"stopped": "stopped", "Stopped": "stopped", "SHUTDOWN": "stopped",
		"shutting-down": "stopping", "LAUNCH_FAILED": "error", "Expired": "expired", "ISOLATED": "expired",
		"CREATING_IMAGE": "pending", "TERMINATING": "deleting", "FREEZING": "frozen", "": "unknown", "weird": "unknown",
	} {
		if got := normalizeCloudState(raw); got != want {
			t.Errorf("normalizeCloudState(%q) = %q, want %q", raw, got, want)
		}
	}
}
