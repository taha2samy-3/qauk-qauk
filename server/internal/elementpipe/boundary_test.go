package elementpipe

import (
	"os/exec"
	"strings"
	"testing"
)

// elementpipe must not import gateway, store, db, service, history, bus or API
// packages, so it stays usable from the API role for previews.
func TestImportBoundary(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", ".").Output()
	if err != nil {
		t.Skipf("go list unavailable: %v", err)
	}
	forbidden := []string{
		"/internal/gateway",
		"/internal/store",
		"/internal/db",
		"/internal/service",
		"/internal/history",
		"/internal/bus",
		"/internal/api",
		"github.com/jackc/pgx",
	}
	for _, dep := range strings.Fields(string(out)) {
		for _, f := range forbidden {
			if strings.Contains(dep, f) {
				t.Errorf("internal/elementpipe depends on %s (forbidden)", dep)
			}
		}
	}
}
