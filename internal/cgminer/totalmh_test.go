package cgminer

import (
	"encoding/json"
	"testing"

	"github.com/hashwatch/hashwatch/internal/model"
)

// Real Nano 3S (cgminer 4.11.1) summary fields read on 2026-11-24.
func TestSummaryTotalMH(t *testing.T) {
	var sum map[string]any
	raw := `{"STATUS":[{"STATUS":"S"}],"SUMMARY":[{"Elapsed":204980,"MHS av":4999122.51,"Total MH":1024720340040.0000}],"id":1}`
	if err := json.Unmarshal([]byte(raw), &sum); err != nil {
		t.Fatal(err)
	}
	s := &model.Snapshot{}
	Build(s, sum, nil, nil, nil, nil)
	if s.Shares.TotalMH == nil || *s.Shares.TotalMH != 1024720340040.0 {
		t.Fatalf("Total MH = %v", s.Shares.TotalMH)
	}
	if s.Uptime.Seconds != 204980 {
		t.Fatalf("Elapsed = %d", s.Uptime.Seconds)
	}
}
