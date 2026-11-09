package httpserver

import (
	"crypto/subtle"
	"net"
	"net/http"
	"strings"
)

// TokenAuth protects next when token is non-empty. Agents send
// "Authorization: Bearer <token>"; browsers use HTTP Basic auth with the
// token as the password (any user name). An empty token disables the check.
func TokenAuth(token string, next http.Handler) http.Handler {
	if token == "" {
		return next
	}
	want := []byte(token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok {
			_, got, _ = r.BasicAuth()
		}
		if subtle.ConstantTimeCompare([]byte(got), want) != 1 {
			w.Header().Set("WWW-Authenticate", `Basic realm="tg-secretary", charset="UTF-8"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// IsLoopback reports whether addr, a "host:port" listen address, is bound to
// the loopback interface only.
func IsLoopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
