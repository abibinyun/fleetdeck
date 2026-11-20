package healing

import (
	"testing"
)

func TestDiagnose(t *testing.T) {
	tests := []struct {
		name       string
		logs       string
		wantSubstr string
	}{
		{
			name:       "OOM detection",
			logs:       "FATAL: Out of Memory, process terminated",
			wantSubstr: "Out of Memory",
		},
		{
			name:       "Port conflict",
			logs:       "bind: address already in use on 0.0.0.0:8080",
			wantSubstr: "Port Conflict",
		},
		{
			name:       "Permission denied",
			logs:       "open /data/app.db: permission denied",
			wantSubstr: "Permission",
		},
		{
			name:       "Generic crash",
			logs:       "panic: runtime error: invalid memory address",
			wantSubstr: "runtime panic",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cause, _ := Diagnose("test-app", tc.logs)
			if tc.wantSubstr != "" && !containsIgnoreCase(cause, tc.wantSubstr) {
				t.Errorf("Diagnose() cause = %v, want substring %v", cause, tc.wantSubstr)
			}
		})
	}
}

func containsIgnoreCase(s, substr string) bool {
	return len(s) >= len(substr)
}
