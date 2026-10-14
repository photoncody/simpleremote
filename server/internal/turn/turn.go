// Package turn builds the ICE server list for a session, including
// short-lived TURN credentials using the "TURN REST API" scheme that coturn
// supports with --use-auth-secret.
package turn

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"strconv"
	"time"

	"github.com/photoncody/simpleremote/internal/proto"
	"github.com/photoncody/simpleremote/server/internal/config"
)

// Servers returns the ICE servers for one peer in a session.
func Servers(c config.ICE, sessionID string, now time.Time) []proto.ICEServer {
	out := []proto.ICEServer{}
	if len(c.STUNURLs) > 0 {
		out = append(out, proto.ICEServer{URLs: c.STUNURLs})
	}
	if len(c.TURNURLs) > 0 && c.TURNSecret != "" {
		user, pass := Credentials(c.TURNSecret, sessionID, now.Add(c.TURNCredTTL))
		out = append(out, proto.ICEServer{URLs: c.TURNURLs, Username: user, Credential: pass})
	}
	return out
}

// Credentials returns a username and password valid until expires.
func Credentials(secret, label string, expires time.Time) (string, string) {
	user := strconv.FormatInt(expires.Unix(), 10) + ":" + label
	mac := hmac.New(sha1.New, []byte(secret))
	mac.Write([]byte(user))
	return user, base64.StdEncoding.EncodeToString(mac.Sum(nil))
}
