// Command mockoidc runs a throwaway OpenID Connect provider for local
// development and end-to-end tests. Do not expose it to a network.
package main

import (
	"flag"
	"log"
	"net/http"
	"strings"

	"github.com/photoncody/simpleremote/internal/mockoidc"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:9999", "listen address")
	issuer := flag.String("issuer", "http://127.0.0.1:9999", "issuer URL (must match how the server reaches it)")
	clientID := flag.String("client-id", "simpleremote", "OAuth client ID")
	secret := flag.String("client-secret", "dev-secret", "OAuth client secret")
	interactive := flag.Bool("interactive", true, "show a form to pick the user")
	email := flag.String("email", "tech@example.com", "default user email")
	name := flag.String("name", "Test Tech", "default user display name")
	groups := flag.String("groups", "helpdesk", "default user groups, comma separated")
	flag.Parse()

	p, err := mockoidc.New(*issuer, *clientID, *secret)
	if err != nil {
		log.Fatal(err)
	}
	p.Interactive = *interactive
	p.SetUser(mockoidc.User{Subject: "sub-" + *email, Email: *email, EmailVerified: true, Name: *name, Groups: strings.Split(*groups, ",")})
	log.Printf("mock OIDC provider at %s (issuer %s)", *addr, *issuer)
	log.Fatal(http.ListenAndServe(*addr, p))
}
