package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wisonwang/aegis/internal/auth"
	"github.com/wisonwang/aegis/internal/config"
	"github.com/wisonwang/aegis/internal/store"
)

// dsResolveStore seeds an in-memory store with one role and one datasource
// whose name ("demo") differs from its id (a uuid). This is exactly the
// scenario that exposed the original bug: callers passing the name instead of
// the id had it persisted verbatim as the foreign key.
func dsResolveStore(t *testing.T) *store.Store {
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.CreateRole(&store.Role{ID: "r-analyst", Name: "analyst"}); err != nil {
		t.Fatalf("role: %v", err)
	}
	if err := st.CreateDataSource(&store.DataSource{ID: "uuid-1", Name: "demo", Type: "sqlite", DSN: ":memory:"}); err != nil {
		t.Fatalf("ds: %v", err)
	}
	return st
}

func dsResolveMux(h *Handler, cfg *config.Config) http.Handler {
	mux := http.NewServeMux()
	a := func(fn http.HandlerFunc) http.HandlerFunc { return Authenticate(cfg, RequireAdmin(fn)) }
	mux.HandleFunc("POST /admin/api/datasources/{id}/masks", a(h.AdminUpsertMask))
	mux.HandleFunc("GET /admin/api/datasources/{id}/masks", a(h.AdminListMasks))
	mux.HandleFunc("POST /admin/api/datasources/{id}/tables/{table}/permissions", a(h.AdminCreateTablePermission))
	mux.HandleFunc("GET /admin/api/datasources/{id}/tables/{table}/permissions", a(h.AdminListTablePermissions))
	return mux
}

func dsResolveAdminTok(t *testing.T, cfg *config.Config) string {
	claims := &auth.Claims{UserID: "admin", Username: "Admin", DisplayName: "Admin", Roles: []string{"admin"}}
	tok, err := auth.GenerateToken(claims, cfg.JWTSecret, cfg.JWTExpiry)
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	return tok
}

func dsResolveCall(mux http.Handler, method, path, token, body string) *httptest.ResponseRecorder {
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, path, nil)
	} else {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
	}
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	return w
}

// TestAdminRoutesResolveByName asserts that admin governance routes accept a
// datasource NAME and resolve it to the canonical UUID, never persisting the
// name as the foreign key (the orphan-FK bug).
func TestAdminRoutesResolveByName(t *testing.T) {
	st := dsResolveStore(t)
	cfg := &config.Config{JWTSecret: "test-secret", JWTExpiry: "24h"}
	h := &Handler{Store: st}
	mux := dsResolveMux(h, cfg)
	tok := dsResolveAdminTok(t, cfg)

	// 1) Upsert a mask via the NAME "demo".
	w := dsResolveCall(mux, http.MethodPost, "/admin/api/datasources/demo/masks",
		tok, `{"role":"analyst","table":"customers","column":"email","strategy":"email"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("upsert mask by name: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// 2) The mask must be stored under the UUID, never under the name.
	masks, err := st.ListColumnMasks("", "uuid-1", "customers")
	if err != nil {
		t.Fatalf("list masks by uuid: %v", err)
	}
	if len(masks) != 1 {
		t.Fatalf("expected 1 mask stored under uuid, got %d", len(masks))
	}
	if masks[0].DataSourceID != "uuid-1" {
		t.Fatalf("mask persisted under datasource_id=%q, expected %q (name leaked as FK)", masks[0].DataSourceID, "uuid-1")
	}
	orphans, _ := st.ListColumnMasks("", "demo", "customers")
	if len(orphans) != 0 {
		t.Fatalf("name was persisted as a foreign key: %d orphan mask(s)", len(orphans))
	}

	// 3) Reading back via the NAME also resolves correctly.
	w = dsResolveCall(mux, http.MethodGet, "/admin/api/datasources/demo/masks", tok, "")
	if w.Code != http.StatusOK {
		t.Fatalf("list masks by name: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// 4) Table permission via NAME resolves to UUID too.
	w = dsResolveCall(mux, http.MethodPost, "/admin/api/datasources/demo/tables/orders/permissions",
		tok, `{"role":"analyst","ops":"SELECT"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("upsert perm by name: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	perms, err := st.ListTablePermissions("r-analyst", "uuid-1", "orders")
	if err != nil {
		t.Fatalf("list perms by uuid: %v", err)
	}
	if len(perms) != 1 || perms[0].DataSourceID != "uuid-1" {
		t.Fatalf("perm not stored under uuid: %+v", perms)
	}

	// 5) Unknown name must 404, not create an orphan.
	w = dsResolveCall(mux, http.MethodPost, "/admin/api/datasources/nope/masks",
		tok, `{"role":"analyst","table":"customers","column":"email","strategy":"email"}`)
	if w.Code != http.StatusNotFound {
		t.Fatalf("unknown name: expected 404, got %d", w.Code)
	}
	// The only mask in the system must still be the one under the UUID.
	remaining, _ := st.ListColumnMasks("", "uuid-1", "")
	if len(remaining) != 1 {
		t.Fatalf("unknown-name call must not create a mask, uuid-1 has %d", len(remaining))
	}
}
