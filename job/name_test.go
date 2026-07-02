package job

import (
	"testing"
)

func TestValidateJobName(t *testing.T) {
	tests := []struct {
		name    string
		wantErr bool
	}{
		{"my-job", false},
		{"My_Job.1", false},
		{"email runner", false},
		{"a", false},
		{"", true},
		{"job with @ invalid", true},
		{"job\nwith\nnewlines", true},
		{"job\twith\ttabs", true},
		{string(make([]byte, 256)), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateJobName(tt.name)
			if tt.wantErr && err == nil {
				t.Error("expected error but got nil")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}
