package mqtt

import (
	"os/exec"
	"strings"
	"testing"
)

// The transport may only reach the gateway through Core/Cluster/StatusSink:
// no database or service code, directly or through a dependency. This keeps
// running it in its own deployment a configuration change (future split).
func TestImportBoundary(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", ".").Output()
	if err != nil {
		t.Skipf("go list unavailable: %v", err)
	}
	forbidden := []string{"/internal/store", "/internal/db", "/internal/service", "/internal/history",
		"/internal/api", "github.com/jackc/pgx"}
	for _, dep := range strings.Fields(string(out)) {
		for _, f := range forbidden {
			if strings.Contains(dep, f) {
				t.Errorf("internal/gateway/mqtt depends on %s", dep)
			}
		}
		if strings.HasSuffix(dep, "/internal/gateway") {
			t.Errorf("internal/gateway/mqtt imports the gateway package itself")
		}
	}
}
