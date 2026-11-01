package api

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"
)

// MinTokenLength is the shortest API token accepted (docs/prod-v1.md §3.1).
const MinTokenLength = 16

// NewToken returns a new random API token: 32 random bytes, base64url.
func NewToken() string {
	b := make([]byte, 32)
	rand.Read(b) // never fails (crypto/rand)
	return base64.RawURLEncoding.EncodeToString(b)
}

// CheckToken reports why token cannot be used as the API token, or nil.
func CheckToken(token string) error {
	if len(token) < MinTokenLength {
		return fmt.Errorf("the API token is %d characters; it needs at least %d (make one with `flowforge token`)",
			len(token), MinTokenLength)
	}
	return nil
}

// RequireToken wraps next so that every request needs the header
// "Authorization: Bearer <token>", except GET /api/health. Others get 401
// (docs/prod-v1.md §3.2).
func RequireToken(token string, next http.Handler) http.Handler {
	// Comparing hashes keeps the comparison constant-time whatever the
	// length of the presented token.
	want := sha256.Sum256([]byte(token))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/health" && (r.Method == http.MethodGet || r.Method == http.MethodHead) {
			next.ServeHTTP(w, r)
			return
		}
		presented, ok := bearer(r.Header.Get("Authorization"))
		got := sha256.Sum256([]byte(presented))
		if !ok || subtle.ConstantTimeCompare(got[:], want[:]) != 1 {
			w.Header().Set("WWW-Authenticate", `Bearer realm="flowforge"`)
			msg := "this API needs the API token in an Authorization: Bearer header"
			if ok {
				msg = "the API token was not accepted"
			}
			writeError(w, http.StatusUnauthorized, msg, nil)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// bearer returns the token of an "Authorization: Bearer <token>" header.
func bearer(header string) (string, bool) {
	scheme, token, ok := strings.Cut(header, " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return "", false
	}
	token = strings.TrimSpace(token)
	return token, token != ""
}
