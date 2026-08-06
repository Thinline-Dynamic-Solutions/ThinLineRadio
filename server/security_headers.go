package main

import (
	"bufio"
	"net"
	"net/http"
	"strings"
)

// SecurityHeadersMiddleware adds security headers to HTTP responses
// Applies safe headers to all responses, and HTML-specific headers only to HTML content
func SecurityHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Wrap the response writer to intercept headers before they're sent
		wrapped := &securityResponseWriter{
			ResponseWriter: w,
		}

		// Call the next handler
		next.ServeHTTP(wrapped, r)
	})
}

// securityResponseWriter wraps http.ResponseWriter to add security headers
// before the response is sent. Implements http.Hijacker for WebSocket support.
type securityResponseWriter struct {
	http.ResponseWriter
	headersWritten bool
}

func (rw *securityResponseWriter) WriteHeader(code int) {
	if !rw.headersWritten {
		rw.addSecurityHeaders()
		rw.headersWritten = true
	}
	rw.ResponseWriter.WriteHeader(code)
}

func (rw *securityResponseWriter) Write(b []byte) (int, error) {
	if !rw.headersWritten {
		rw.addSecurityHeaders()
		rw.headersWritten = true
	}
	return rw.ResponseWriter.Write(b)
}

// Hijack implements http.Hijacker to support WebSocket connections
func (rw *securityResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hijacker, ok := rw.ResponseWriter.(http.Hijacker); ok {
		return hijacker.Hijack()
	}
	return nil, nil, http.ErrNotSupported
}

// Flush implements http.Flusher so streaming handlers (NDJSON/SSE) work through this middleware.
func (rw *securityResponseWriter) Flush() {
	if flusher, ok := rw.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (rw *securityResponseWriter) addSecurityHeaders() {
	// Check content type to determine which headers to apply
	contentType := rw.Header().Get("Content-Type")
	isHTML := strings.HasPrefix(contentType, "text/html")

	// Always apply these headers (safe for all content types)
	rw.Header().Set("X-Content-Type-Options", "nosniff")
	rw.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")

	// Only apply frame protection and XSS protection to HTML content
	// This prevents breaking JSON API responses while protecting HTML pages
	if isHTML {
		// Use SAMEORIGIN instead of DENY to allow same-origin iframe embedding
		// This preserves functionality while preventing cross-origin clickjacking
		rw.Header().Set("X-Frame-Options", "SAMEORIGIN")
		rw.Header().Set("X-XSS-Protection", "1; mode=block")
	}
}
