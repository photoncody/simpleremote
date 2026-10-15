package httpx

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"
)

func TestRealIP(t *testing.T) {
	trusted := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}
	var got string
	h := RealIP(trusted)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { got = ClientIP(r) }))

	cases := []struct {
		remote, xff, want string
	}{
		{"203.0.113.9:1234", "1.2.3.4", "203.0.113.9"},             // untrusted peer: ignore XFF
		{"10.0.0.2:1234", "1.2.3.4", "1.2.3.4"},                    // trusted proxy
		{"10.0.0.2:1234", "6.6.6.6, 1.2.3.4, 10.0.0.3", "1.2.3.4"}, // rightmost untrusted hop
		{"10.0.0.2:1234", "", "10.0.0.2"},
		{"[::ffff:10.0.0.2]:1", "garbage", "10.0.0.2"},
	}
	for _, c := range cases {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = c.remote
		if c.xff != "" {
			r.Header.Set("X-Forwarded-For", c.xff)
		}
		h.ServeHTTP(httptest.NewRecorder(), r)
		if got != c.want {
			t.Errorf("remote=%s xff=%q: got %s, want %s", c.remote, c.xff, got, c.want)
		}
	}
}

func TestLimiter(t *testing.T) {
	l := NewLimiter(time.Hour, 3)
	for i := 0; i < 3; i++ {
		if !l.Allow("a") {
			t.Fatalf("request %d rejected", i)
		}
	}
	if l.Allow("a") {
		t.Fatal("4th request allowed")
	}
	if !l.Allow("b") {
		t.Fatal("other key rejected")
	}
}

func TestSameOrigin(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	if !SameOrigin(r, "https://x.example") {
		t.Fatal("no Origin header should pass")
	}
	r.Header.Set("Origin", "https://evil.example")
	if SameOrigin(r, "https://x.example") {
		t.Fatal("cross origin passed")
	}
}
