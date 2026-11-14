package app

import (
	"context"
	"crypto/subtle"
	"net/http"
	"strings"
	"time"
)

const csrfCookie = "hw_csrf"

type csrfCtxKey struct{}

func isUnsafeMethod(m string) bool {
	switch m {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

func csrfExempt(r *http.Request) bool {
	// Second launch of the app asks the running copy to show its window.
	// That POST comes from the local process, not the dashboard, and is
	// already limited to this computer.
	return r.Method == http.MethodPost && r.URL.Path == "/api/show"
}

func (s *Server) csrfToken(r *http.Request) string {
	if v, ok := r.Context().Value(csrfCtxKey{}).(string); ok {
		return v
	}
	if c, err := r.Cookie(csrfCookie); err == nil && c.Value != "" {
		return c.Value
	}
	return ""
}

func setCSRFCookie(w http.ResponseWriter, r *http.Request, tok string) {
	secure := r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" || viaTunnel(r)
	http.SetCookie(w, &http.Cookie{
		Name:     csrfCookie,
		Value:    tok,
		Path:     "/",
		HttpOnly: false,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int((7 * 24 * time.Hour) / time.Second),
	})
}

// csrfWrap requires a matching anti-forgery token on every state-changing
// request (header X-CSRF-Token must equal the hw_csrf cookie). GET /api/state
// issues the cookie and also returns the token so the dashboard can send it.
func (s *Server) csrfWrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tok := ""
		if c, err := r.Cookie(csrfCookie); err == nil {
			tok = c.Value
		}
		if tok == "" {
			tok = randToken(32)
		}
		setCSRFCookie(w, r, tok)
		r = r.WithContext(context.WithValue(r.Context(), csrfCtxKey{}, tok))
		if isUnsafeMethod(r.Method) && !csrfExempt(r) {
			got := strings.TrimSpace(r.Header.Get("X-CSRF-Token"))
			if got == "" || subtle.ConstantTimeCompare([]byte(got), []byte(tok)) != 1 {
				writeJSON(w, 403, map[string]string{"error": "Missing or invalid security token. Reload the page and try again."})
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
