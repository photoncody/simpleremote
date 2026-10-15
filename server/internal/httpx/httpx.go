// Package httpx holds small HTTP helpers shared by the server's handlers.
package httpx

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// WriteJSON writes v as a JSON response.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Debug("write json", "err", err)
	}
}

// Error writes a JSON error response.
func Error(w http.ResponseWriter, status int, msg string) {
	WriteJSON(w, status, map[string]string{"error": msg})
}

// ReadJSON decodes a request body of at most 64 KiB into v.
func ReadJSON(r *http.Request, v any) error {
	body := http.MaxBytesReader(nil, r.Body, 64<<10)
	dec := json.NewDecoder(body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return errors.New("unexpected data after JSON body")
	}
	return nil
}

// ---- client IP -------------------------------------------------------------

type ipKey struct{}

// RealIP stores the client's address in the request context. X-Forwarded-For
// is only honored when the direct peer is a trusted proxy, and then the
// rightmost untrusted hop is used.
func RealIP(trusted []netip.Prefix) func(http.Handler) http.Handler {
	isTrusted := func(a netip.Addr) bool {
		for _, p := range trusted {
			if p.Contains(a) {
				return true
			}
		}
		return false
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := peerAddr(r.RemoteAddr)
			if ip.IsValid() && isTrusted(ip) {
				hops := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
				for i := len(hops) - 1; i >= 0; i-- {
					a, err := netip.ParseAddr(strings.TrimSpace(hops[i]))
					if err != nil {
						break
					}
					ip = a.Unmap()
					if !isTrusted(ip) {
						break
					}
				}
			}
			ctx := context.WithValue(r.Context(), ipKey{}, ip)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func peerAddr(remote string) netip.Addr {
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		host = remote
	}
	a, _ := netip.ParseAddr(host)
	return a.Unmap()
}

// ClientIP returns the address found by RealIP.
func ClientIP(r *http.Request) string {
	if a, ok := r.Context().Value(ipKey{}).(netip.Addr); ok && a.IsValid() {
		return a.String()
	}
	return peerAddr(r.RemoteAddr).String()
}

// ---- security headers ------------------------------------------------------

// SecurityHeaders sets conservative headers on every response.
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data: blob:; style-src 'self' 'unsafe-inline'; "+
			"connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'; object-src 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
			h.Set("Strict-Transport-Security", "max-age=31536000")
		}
		next.ServeHTTP(w, r)
	})
}

// ---- rate limiting ---------------------------------------------------------

// Limiter is a per-key token bucket limiter.
type Limiter struct {
	mu      sync.Mutex
	every   rate.Limit
	burst   int
	buckets map[string]*bucket
}

type bucket struct {
	lim  *rate.Limiter
	seen time.Time
}

// NewLimiter allows burst events immediately and then one every interval.
func NewLimiter(interval time.Duration, burst int) *Limiter {
	return &Limiter{every: rate.Every(interval), burst: burst, buckets: map[string]*bucket{}}
}

// Allow reports whether key may proceed.
func (l *Limiter) Allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	b, ok := l.buckets[key]
	if !ok {
		if len(l.buckets) > 100_000 {
			l.sweepLocked(now)
		}
		b = &bucket{lim: rate.NewLimiter(l.every, l.burst)}
		l.buckets[key] = b
	}
	b.seen = now
	return b.lim.AllowN(now, 1)
}

// Sweep drops idle buckets. Call it periodically.
func (l *Limiter) Sweep() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.sweepLocked(time.Now())
}

func (l *Limiter) sweepLocked(now time.Time) {
	for k, b := range l.buckets {
		if now.Sub(b.seen) > 30*time.Minute {
			delete(l.buckets, k)
		}
	}
}

// Middleware rejects requests over the limit, keyed by client IP.
func (l *Limiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !l.Allow(ClientIP(r)) {
			w.Header().Set("Retry-After", "60")
			Error(w, http.StatusTooManyRequests, "too many requests, try again in a minute")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ---- same-origin check ------------------------------------------------------

// SameOrigin reports whether the request's Origin header (if any) matches
// publicOrigin, such as "https://support.example.com".
func SameOrigin(r *http.Request, publicOrigin string) bool {
	o := r.Header.Get("Origin")
	return o == "" || strings.EqualFold(o, publicOrigin)
}
