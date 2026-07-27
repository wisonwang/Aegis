package nl2sql

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func newTestServer(t *testing.T, fake *minimalFakeServer) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		fake.lastBody = r.Method + " " + r.URL.Path + " | " + string(b) + " | auth=" + r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(fake.code)
		io.WriteString(w, fake.respond)
	}))
}
