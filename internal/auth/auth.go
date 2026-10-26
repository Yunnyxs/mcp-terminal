package auth

import (
	"crypto/subtle"
	"fmt"
	"net/http"
)

const MinTokenLength = 32

func ValidateToken(token string) error {
	if len(token) < MinTokenLength {
		return fmt.Errorf("token must be at least %d characters", MinTokenLength)
	}
	for _, c := range token {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_':
		default:
			return fmt.Errorf("token contains an invalid character %q (only A-Z, a-z, 0-9, - and _ are allowed)", c)
		}
	}
	return nil
}

func WithToken(token string, next http.Handler) (http.Handler, error) {
	if err := ValidateToken(token); err != nil {
		return nil, err
	}
	prefix := "/" + token
	const suffix = "/mcp"
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path

		ok := len(p) == len(prefix)+len(suffix) &&
			subtle.ConstantTimeCompare([]byte(p[:len(prefix)]), []byte(prefix)) == 1 &&
			p[len(prefix):] == suffix
		if !ok {
			http.NotFound(w, r)
			return
		}
		next.ServeHTTP(w, r)
	}), nil
}
