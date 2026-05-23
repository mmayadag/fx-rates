package seed

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestFSContainsProviderData(t *testing.T) {
	entries, err := FS.ReadDir("data/providers")
	if err != nil {
		t.Fatalf("ReadDir(data/providers) returned error: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("expected at least one provider JSON in embedded FS, got 0")
	}

	var foundECB bool
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if !strings.HasSuffix(e.Name(), ".json") {
			t.Errorf("unexpected non-json entry %q", e.Name())
		}
		if e.Name() == "ecb.json" {
			foundECB = true
		}
	}
	if !foundECB {
		t.Fatal("expected ecb.json in embedded provider seed data")
	}
}

func TestECBSeedFileIsValidJSON(t *testing.T) {
	data, err := FS.ReadFile("data/providers/ecb.json")
	if err != nil {
		t.Fatalf("ReadFile ecb.json: %v", err)
	}

	var payload struct {
		Key  string `json:"key"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("ecb.json is not valid JSON: %v", err)
	}
	if payload.Key != "ECB" {
		t.Errorf("ecb.json key = %q, want ECB", payload.Key)
	}
	if payload.Name == "" {
		t.Error("ecb.json name must not be empty")
	}
}

func TestFSReadFileMissingReturnsError(t *testing.T) {
	if _, err := FS.ReadFile("data/providers/nope.json"); err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}
