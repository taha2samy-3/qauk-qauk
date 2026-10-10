// Package cluster spreads work (MQTT connections) over live gateways with
// weighted Rendezvous Hashing (highest random weight). Every gateway computes
// the same assignment on its own from the same member list: no coordinator.
// When a member leaves, only its keys move; when one joins, it takes an even
// share from the others.
package cluster

import (
	"math"
	"slices"

	"github.com/cespare/xxhash/v2"
)

// Member is a live gateway that can take work.
type Member struct {
	ID     string
	Weight float64 // relative capacity; <= 0 counts as 1
}

// Owners returns the IDs of the k members that own key, best first. Slot s of
// the key belongs to Owners(...)[s].
func Owners(key string, members []Member, k int) []string {
	type scored struct {
		id    string
		score float64
	}
	s := make([]scored, 0, len(members))
	for _, m := range members {
		s = append(s, scored{m.ID, Score(key, m)})
	}
	slices.SortFunc(s, func(a, b scored) int {
		switch {
		case a.score > b.score:
			return -1
		case a.score < b.score:
			return 1
		}
		if a.id < b.id { // ties are vanishingly rare; break them the same way everywhere
			return -1
		}
		return 1
	})
	k = min(k, len(s))
	out := make([]string, k)
	for i := range k {
		out[i] = s[i].id
	}
	return out
}

// Score is the weighted HRW score of a member for a key: -w / ln(u), where
// u ∈ (0,1) comes from a 64-bit hash of key and member. Higher wins.
func Score(key string, m Member) float64 {
	w := m.Weight
	if w <= 0 {
		w = 1
	}
	h := xxhash.Sum64String(key + "\x00" + m.ID)
	u := (float64(h>>11) + 0.5) / (1 << 53) // (0,1), never 0 or 1
	return -w / math.Log(u)
}
