package model

import (
	"fmt"
	"sort"
	"strings"
)

// WorkerLabel is the public name for a pool user: the suffix after the last
// '.', or "Worker N" when the user is a bare payout address (no suffix).
// The payout address itself is never returned.
func WorkerLabel(user string, index int) string {
	u := strings.TrimSpace(user)
	if i := strings.LastIndex(u, "."); i >= 0 && i < len(u)-1 {
		if suf := strings.TrimSpace(u[i+1:]); suf != "" {
			return suf
		}
	}
	if index < 1 {
		index = 1
	}
	return fmt.Sprintf("Worker %d", index)
}

// PayoutReplacements maps raw pool user strings (and the address prefix
// before the last '.') to their public WorkerLabel. Longer keys come first.
func PayoutReplacements(users []string) [][2]string {
	var out [][2]string
	seen := map[string]bool{}
	for i, u := range users {
		u = strings.TrimSpace(u)
		if u == "" {
			continue
		}
		lab := WorkerLabel(u, i+1)
		add := func(from, to string) {
			if from == "" || from == to || seen[from] {
				return
			}
			seen[from] = true
			out = append(out, [2]string{from, to})
		}
		add(u, lab)
		if i := strings.LastIndex(u, "."); i >= 20 {
			add(u[:i], lab)
		}
	}
	sort.Slice(out, func(i, j int) bool { return len(out[i][0]) > len(out[j][0]) })
	return out
}

// ReplacePayouts replaces every known payout address in s with its public label.
func ReplacePayouts(s string, reps [][2]string) string {
	for _, r := range reps {
		if r[0] != "" && strings.Contains(s, r[0]) {
			s = strings.ReplaceAll(s, r[0], r[1])
		}
	}
	return s
}

// RedactSnapshotForView returns a copy of src with pool users (and any
// mm_fields that embed them) replaced by WorkerLabel. src is not modified.
func RedactSnapshotForView(src *Snapshot) *Snapshot {
	if src == nil {
		return nil
	}
	d := *src
	users := make([]string, 0, len(src.Pools))
	if src.Pools != nil {
		d.Pools = append([]Pool(nil), src.Pools...)
		for i := range d.Pools {
			users = append(users, src.Pools[i].User)
			d.Pools[i].User = WorkerLabel(src.Pools[i].User, i+1)
		}
	}
	reps := PayoutReplacements(users)
	if src.MMFields != nil {
		d.MMFields = make(map[string]string, len(src.MMFields))
		for k, v := range src.MMFields {
			d.MMFields[k] = ReplacePayouts(v, reps)
		}
	}
	d.QueryErrors = append([]string(nil), src.QueryErrors...)
	for i, e := range d.QueryErrors {
		d.QueryErrors[i] = ReplacePayouts(e, reps)
	}
	d.Warnings = append([]string(nil), src.Warnings...)
	for i, e := range d.Warnings {
		d.Warnings[i] = ReplacePayouts(e, reps)
	}
	return &d
}
