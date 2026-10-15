package turn

import (
	"testing"
	"time"

	"github.com/photoncody/simpleremote/server/internal/config"
)

func TestCredentials(t *testing.T) {
	// Known answer computed with:
	//   echo -n "1700000000:s1" | openssl dgst -sha1 -hmac secret -binary | base64
	user, pass := Credentials("secret", "s1", time.Unix(1700000000, 0))
	if user != "1700000000:s1" {
		t.Fatalf("user = %s", user)
	}
	if pass != "LkPj316IePGc8iXpodbQTmNcZfI=" {
		t.Fatalf("pass = %s", pass)
	}
}

func TestServers(t *testing.T) {
	c := config.ICE{STUNURLs: []string{"stun:a:3478"}, TURNURLs: []string{"turn:a:3478"}, TURNSecret: "x", TURNCredTTL: time.Hour}
	s := Servers(c, "sess", time.Now())
	if len(s) != 2 || s[1].Username == "" || s[1].Credential == "" || s[0].Username != "" {
		t.Fatalf("servers = %+v", s)
	}
	if got := Servers(config.ICE{}, "sess", time.Now()); len(got) != 0 {
		t.Fatalf("empty config gave %+v", got)
	}
}
