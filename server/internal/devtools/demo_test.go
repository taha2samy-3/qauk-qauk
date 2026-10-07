package devtools

import (
	"encoding/json"
	"testing"
)

func TestShouldEchoOnlyUserCommands(t *testing.T) {
	const dev = "01a117b6-2a20-7d90-96b8-fee5a72b1824"
	cases := map[string]bool{
		// a user command: user_id is a number
		`{"element_id":"e","message":{"relay":"ON"},"auth":{"user_id":1,"username":"admin"}}`: true,
		// the same device's other socket: user_id is the device id -> never echo (echo loop)
		`{"element_id":"e","message":{"relay":"OFF"},"auth":{"user_id":"` + dev + `","username":"Weather station"}}`: false,
		// another device's id would still be echoed (only self is filtered)
		`{"element_id":"e","message":{"v":1},"auth":{"user_id":"other","username":"x"}}`: true,
		`{"element_id":"","message":{"v":1}}`:                                            false,
		`{"element_id":"e"}`:                                                             false,
	}
	for raw, want := range cases {
		var f incomingFrame
		if err := json.Unmarshal([]byte(raw), &f); err != nil {
			t.Fatal(err)
		}
		if got := shouldEcho(f, dev); got != want {
			t.Errorf("%s: got %v want %v", raw, got, want)
		}
	}
}
