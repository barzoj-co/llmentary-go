// SPDX-License-Identifier: MIT

package llmentary

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

type outcomeVectorCase struct {
	Name       string          `json:"name"`
	Outcome    string          `json:"outcome"`
	ClientOnly bool            `json:"client_only"`
	Call       json.RawMessage `json:"call"`
}

// TestCallOutcomeVector runs the shared DOG-58 vector, including the
// client_only recorded-outcome cases that the proxy classifier skips.
func TestCallOutcomeVector(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "control", "call_outcomes.json"))
	if err != nil {
		t.Fatal(err)
	}
	var vector struct {
		Version int                 `json:"version"`
		Cases   []outcomeVectorCase `json:"cases"`
	}
	if err := json.Unmarshal(raw, &vector); err != nil {
		t.Fatal(err)
	}
	if vector.Version != 1 {
		t.Fatalf("version=%d want 1", vector.Version)
	}
	if len(vector.Cases) != 15 {
		t.Fatalf("cases=%d want 15", len(vector.Cases))
	}

	for _, tc := range vector.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			var call Call
			if err := json.Unmarshal(tc.Call, &call); err != nil {
				t.Fatal(err)
			}
			if call.EndedAt.IsZero() {
				t.Fatalf("vector case %q must be settled: %s", tc.Name, tc.Call)
			}
			if got := call.Outcome(); got != tc.Outcome {
				t.Fatalf("Outcome()=%q want %q", got, tc.Outcome)
			}
		})
	}
}
