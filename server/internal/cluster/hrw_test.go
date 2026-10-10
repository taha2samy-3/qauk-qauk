package cluster

import (
	"fmt"
	"math"
	"testing"
)

func members(n int) []Member {
	out := make([]Member, n)
	for i := range out {
		out[i] = Member{ID: fmt.Sprintf("gw-%d", i), Weight: 1}
	}
	return out
}

func owner(key string, ms []Member) string { return Owners(key, ms, 1)[0] }

func TestEvenSpread(t *testing.T) {
	for _, n := range []int{3, 5, 10} {
		ms := members(n)
		count := map[string]int{}
		const keys = 20000
		for i := range keys {
			count[owner(fmt.Sprintf("conn-%d", i), ms)]++
		}
		want := float64(keys) / float64(n)
		for id, c := range count {
			if math.Abs(float64(c)-want)/want > 0.06 {
				t.Errorf("n=%d: %s owns %d, want about %.0f", n, id, c, want)
			}
		}
	}
}

func TestOnlyTheLeaversKeysMove(t *testing.T) {
	ms := members(5)
	before := map[string]string{}
	for i := range 5000 {
		k := fmt.Sprintf("conn-%d", i)
		before[k] = owner(k, ms)
	}
	gone := ms[2]
	left := append(append([]Member{}, ms[:2]...), ms[3:]...)
	moved := 0
	for k, was := range before {
		now := owner(k, left)
		if was != gone.ID && now != was {
			t.Fatalf("%s moved from %s to %s although %s was not removed", k, was, now, was)
		}
		if was == gone.ID {
			moved++
		}
	}
	if moved == 0 {
		t.Fatal("the removed member owned nothing?")
	}
	// a new member takes a share only from others, never reshuffles among them
	joined := append(append([]Member{}, ms...), Member{ID: "gw-new", Weight: 1})
	for k, was := range before {
		if now := owner(k, joined); now != was && now != "gw-new" {
			t.Fatalf("%s moved between old members (%s → %s) on a join", k, was, now)
		}
	}
}

func TestWeights(t *testing.T) {
	ms := []Member{{ID: "small", Weight: 1}, {ID: "big", Weight: 3}}
	count := map[string]int{}
	for i := range 20000 {
		count[owner(fmt.Sprintf("conn-%d", i), ms)]++
	}
	ratio := float64(count["big"]) / float64(count["small"])
	if ratio < 2.7 || ratio > 3.3 {
		t.Fatalf("weight 3 vs 1 gave ratio %.2f (%v)", ratio, count)
	}
}

func TestTopKAndAgreement(t *testing.T) {
	ms := members(4)
	o := Owners("conn-x", ms, 2)
	if len(o) != 2 || o[0] == o[1] {
		t.Fatalf("top 2 = %v", o)
	}
	// order of the member list doesn't matter: every gateway agrees
	rev := []Member{ms[3], ms[1], ms[0], ms[2]}
	if r := Owners("conn-x", rev, 2); r[0] != o[0] || r[1] != o[1] {
		t.Fatalf("different member order gave %v vs %v", r, o)
	}
	if got := Owners("conn-x", ms[:1], 3); len(got) != 1 {
		t.Fatalf("k larger than members: %v", got)
	}
	if got := Owners("conn-x", nil, 1); len(got) != 0 {
		t.Fatalf("no members: %v", got)
	}
}
