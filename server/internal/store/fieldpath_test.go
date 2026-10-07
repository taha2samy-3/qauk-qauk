package store

import (
	"errors"
	"slices"
	"testing"
)

func TestParseFieldPath(t *testing.T) {
	for in, want := range map[string][]string{
		"value":           {"value"},
		"gps.lat":         {"gps", "lat"},
		"sensors[0].temp": {"sensors", "0", "temp"},
		"a[2][3]":         {"a", "2", "3"},
		"temp-c":          {"temp-c"},
	} {
		got, err := ParseFieldPath(in)
		if err != nil || !slices.Equal(got, want) {
			t.Errorf("%q -> %v, %v; want %v", in, got, err, want)
		}
	}
	for _, bad := range []string{"", ".a", "a.", "a..b", "a[x]", "a]", "a b", "a;drop", "a'b", `a"b`, "a[0", string(make([]byte, 201))} {
		if _, err := ParseFieldPath(bad); !errors.Is(err, ErrBadFieldPath) {
			t.Errorf("%q accepted", bad)
		}
	}
}
