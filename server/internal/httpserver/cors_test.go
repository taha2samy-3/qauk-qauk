package httpserver

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAPICORS(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })
	do := func(h http.Handler, method, path, origin string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, nil)
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		if method == http.MethodOptions {
			r.Header.Set("Access-Control-Request-Method", "POST")
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}

	h := apiCORS([]string{"https://wall.example/"})(ok)
	// a preflight from an allowed origin is answered here
	w := do(h, http.MethodOptions, "/api/v1/elements/x/commands", "https://wall.example")
	if w.Code != http.StatusNoContent || w.Header().Get("Access-Control-Allow-Origin") != "https://wall.example" ||
		w.Header().Get("Access-Control-Allow-Headers") != "Authorization, Content-Type" {
		t.Fatalf("preflight: %d %v", w.Code, w.Header())
	}
	// never with credentials: a session cookie can't ride along
	if w.Header().Get("Access-Control-Allow-Credentials") != "" {
		t.Fatal("credentials must not be allowed")
	}
	// a request from an allowed origin gets the header and reaches the API
	if w := do(h, http.MethodGet, "/api/v1/elements", "https://wall.example"); w.Code != http.StatusTeapot || w.Header().Get("Access-Control-Allow-Origin") == "" {
		t.Fatalf("get: %d %v", w.Code, w.Header())
	}
	// other origins, and paths outside the API, get nothing
	for _, c := range []struct{ path, origin string }{{"/api/v1/elements", "https://evil.example"}, {"/healthz", "https://wall.example"}} {
		if w := do(h, http.MethodGet, c.path, c.origin); w.Header().Get("Access-Control-Allow-Origin") != "" {
			t.Errorf("%s from %s: %v", c.path, c.origin, w.Header())
		}
	}
	// "*" allows any origin; no origins configured: no CORS at all
	if w := do(apiCORS([]string{"*"})(ok), http.MethodGet, "/api/v1/alerts", "https://any.example"); w.Header().Get("Access-Control-Allow-Origin") != "https://any.example" {
		t.Fatalf("wildcard: %v", w.Header())
	}
	if w := do(apiCORS(nil)(ok), http.MethodOptions, "/api/v1/alerts", "https://any.example"); w.Code != http.StatusTeapot {
		t.Fatalf("disabled: %d", w.Code)
	}
}
