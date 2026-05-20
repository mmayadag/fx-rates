package main

import "testing"

func TestNormalizeProviderFilter(t *testing.T) {
	tests := []struct {
		input   string
		want    string
		wantErr bool
	}{
		{input: "", want: "ECB"},
		{input: "ecb", want: "ECB"},
		{input: " ECB ", want: "ECB"},
		{input: "CURAPI", wantErr: true},
	}

	for _, tt := range tests {
		got, err := normalizeProviderFilter(tt.input)
		if tt.wantErr {
			if err == nil {
				t.Fatalf("normalizeProviderFilter(%q) expected error", tt.input)
			}
			continue
		}
		if err != nil {
			t.Fatalf("normalizeProviderFilter(%q) unexpected error: %v", tt.input, err)
		}
		if got != tt.want {
			t.Fatalf("normalizeProviderFilter(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
