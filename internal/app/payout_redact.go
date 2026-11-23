package app

import (
	"encoding/json"

	"github.com/hashwatch/hashwatch/internal/model"
)

// scrubLivePayouts walks the /api/live envelope and replaces any remaining
// payout address strings (e.g. in last_err or mm_fields) with WorkerLabel.
// The cached snapshot itself is not modified.
func scrubLivePayouts(out map[string]any, src *model.Snapshot) {
	if src == nil || out == nil {
		return
	}
	users := make([]string, 0, len(src.Pools))
	for _, p := range src.Pools {
		users = append(users, p.User)
	}
	reps := model.PayoutReplacements(users)
	if len(reps) == 0 {
		return
	}
	raw, err := json.Marshal(out)
	if err != nil {
		return
	}
	s := string(raw)
	for _, r := range reps {
		s = model.ReplacePayouts(s, [][2]string{r})
	}
	var next map[string]any
	if err := json.Unmarshal([]byte(s), &next); err != nil {
		return
	}
	for k, v := range next {
		out[k] = v
	}
}
