package auth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestValidateToken(t *testing.T) {
	good := strings.Repeat("a", MinTokenLength)
	for _, tok := range []string{good, strings.Repeat("Ab0-_", 7), strings.Repeat("f", 64)} {
		if err := ValidateToken(tok); err != nil {
			t.Errorf("%q should be valid: %v", tok, err)
		}
	}
	cases := []struct{ name, tok, want string }{
		{"empty", "", "at least 32"},
		{"too short", strings.Repeat("a", MinTokenLength-1), "at least 32"},
		{"contains slash", strings.Repeat("a", 40) + "/x", "invalid character '/'"},
		{"contains space", strings.Repeat("a", 40) + " ", "invalid character"},
		{"contains dot", strings.Repeat("a", 40) + ".", "invalid character '.'"},
		{"contains percent", strings.Repeat("a", 40) + "%2f", "invalid character '%'"},
		{"non-ASCII", strings.Repeat("a", 40) + "é", "invalid character"},
	}
	for _, c := range cases {
		if err := ValidateToken(c.tok); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want containing %q", c.name, err, c.want)
		}
	}
}

func TestWithTokenRejectsWeakToken(t *testing.T) {
	if h, err := WithToken("short", http.NotFoundHandler()); err == nil || h != nil {
		t.Errorf("weak token must be rejected on creation: %v %v", h, err)
	}
}

func TestWithTokenRouting(t *testing.T) {
	token := strings.Repeat("a", 31) + "b"
	reached := 0
	h, err := WithToken(token, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached++
		w.WriteHeader(http.StatusTeapot)
	}))
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name, path string
		want       int
	}{
		{"correct path", "/" + token + "/mcp", http.StatusTeapot},
		{"correct path with query", "/" + token + "/mcp?x=1", http.StatusTeapot},
		{"token off by one character", "/" + strings.Repeat("a", 32) + "/mcp", http.StatusNotFound},
		{"wrong token of same length", "/" + strings.Repeat("c", 32) + "/mcp", http.StatusNotFound},
		{"no token", "/mcp", http.StatusNotFound},
		{"root", "/", http.StatusNotFound},
		{"different suffix", "/" + token + "/other", http.StatusNotFound},
		{"no suffix", "/" + token, http.StatusNotFound},
		{"trailing slash", "/" + token + "/mcp/", http.StatusNotFound},
		{"longer suffix", "/" + token + "/mcpx", http.StatusNotFound},
		{"shorter token", "/" + token[:10] + "/mcp", http.StatusNotFound},
		{"longer token", "/" + token + "x/mcp", http.StatusNotFound},
		{"token twice", "/" + token + "/" + token + "/mcp", http.StatusNotFound},
	}
	for _, c := range cases {
		before := reached
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, c.path, nil))
		if rec.Code != c.want {
			t.Errorf("%s: status = %d, want %d", c.name, rec.Code, c.want)
		}
		if (rec.Code == http.StatusTeapot) != (reached == before+1) {
			t.Errorf("%s: next handler call does not match status", c.name)
		}
	}
}

func TestWithTokenNotFoundLeaksNothing(t *testing.T) {
	token := strings.Repeat("s", 40)
	h, _ := WithToken(token, http.NotFoundHandler())
	for _, path := range []string{"/" + strings.Repeat("x", 40) + "/mcp", "/", "/" + token + "/zzz"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if body := rec.Body.String(); strings.Contains(body, token) || strings.TrimSpace(body) != "404 page not found" {
			t.Errorf("%s: body = %q", path, body)
		}
	}
}
