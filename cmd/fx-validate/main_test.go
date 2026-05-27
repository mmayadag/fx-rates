package main

import "testing"

func TestValidateDateFlag(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		wantErr bool
	}{
		{"empty is allowed", "", false},
		{"valid date", "2024-10-01", false},
		{"wrong format", "01-10-2024", true},
		{"not a date", "yesterday", true},
		{"impossible date", "2024-13-40", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateDateFlag("date-from", tt.value)
			if (err != nil) != tt.wantErr {
				t.Fatalf("validateDateFlag(%q) error = %v, wantErr %v", tt.value, err, tt.wantErr)
			}
		})
	}
}

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
