package store

import (
	"errors"
	"regexp"
	"strings"
)

// A field path selects an attribute inside an element message, e.g.
// "temperature", "gps.lat" or "sensors[0].temp".
var fieldPathRe = regexp.MustCompile(`^[A-Za-z0-9_@$-]+(\.[A-Za-z0-9_@$-]+|\[[0-9]+\])*$`)

var ErrBadFieldPath = errors.New("invalid field path")

// ParseFieldPath turns "sensors[0].temp" into ["sensors", "0", "temp"], the
// text[] form Postgres uses for `payload #> path`. It never builds SQL text.
func ParseFieldPath(p string) ([]string, error) {
	if len(p) == 0 || len(p) > 200 || !fieldPathRe.MatchString(p) {
		return nil, ErrBadFieldPath
	}
	p = strings.NewReplacer("[", ".", "]", "").Replace(p)
	return strings.Split(p, "."), nil
}
