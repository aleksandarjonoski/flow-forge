package api

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aleksandarjonoski/flow-forge/backend/internal/engine"
)

func TestRequireToken(t *testing.T) {
	const token = "0123456789abcdef-token"
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })
	h := RequireToken(token, ok)

	cases := []struct {
		name, method, path, auth string
		want                     int
		wantMsg                  string
	}{
		{"right token", "GET", "/api/flows", "Bearer " + token, http.StatusTeapot, ""},
		{"scheme in any case", "POST", "/api/flows/run", "bearer " + token, http.StatusTeapot, ""},
		{"no header", "GET", "/api/flows", "", http.StatusUnauthorized, "needs the API token"},
		{"wrong token", "GET", "/api/flows", "Bearer nope", http.StatusUnauthorized, "not accepted"},
		{"token prefix", "GET", "/api/flows", "Bearer " + token[:10], http.StatusUnauthorized, "not accepted"},
		{"basic auth", "GET", "/api/flows", "Basic " + token, http.StatusUnauthorized, "needs the API token"},
		{"empty bearer", "GET", "/api/flows", "Bearer ", http.StatusUnauthorized, "needs the API token"},
		{"health is exempt", "GET", "/api/health", "", http.StatusTeapot, ""},
		{"only GET health", "POST", "/api/health", "", http.StatusUnauthorized, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			if tc.auth != "" {
				req.Header.Set("Authorization", tc.auth)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d", rec.Code, tc.want)
			}
			if tc.want != http.StatusUnauthorized {
				return
			}
			if got := rec.Header().Get("WWW-Authenticate"); !strings.HasPrefix(got, "Bearer") {
				t.Errorf("WWW-Authenticate = %q", got)
			}
			if !strings.Contains(rec.Body.String(), tc.wantMsg) {
				t.Errorf("body = %q, want %q", rec.Body.String(), tc.wantMsg)
			}
		})
	}
}

func TestNewToken(t *testing.T) {
	a, b := NewToken(), NewToken()
	if a == b {
		t.Fatal("two tokens are equal")
	}
	raw, err := base64.RawURLEncoding.DecodeString(a)
	if err != nil || len(raw) != 32 {
		t.Errorf("token %q: %d bytes, %v; want 32 bytes of base64url", a, len(raw), err)
	}
	if err := CheckToken(a); err != nil {
		t.Errorf("CheckToken(new token) = %v", err)
	}
	if err := CheckToken("short"); err == nil || !strings.Contains(err.Error(), "at least 16") {
		t.Errorf("CheckToken(short) = %v", err)
	}
}

func TestNewHandler_Token(t *testing.T) {
	fx := newLib(t)
	h := NewHandler(engine.DefaultRegistry, Options{Store: fx.store, Host: fx.host, Token: "0123456789abcdef"})
	for path, want := range map[string]int{"/api/health": 200, "/api/flows": 401} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		if rec.Code != want {
			t.Errorf("GET %s = %d, want %d", path, rec.Code, want)
		}
	}
}
