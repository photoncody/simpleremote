# Common development tasks. CI runs the same steps (see .github/workflows).

VERSION ?= dev
GOFLAGS_BUILD = -trimpath -ldflags "-s -w -X main.version=$(VERSION)"

.PHONY: all web server agent test test-web e2e docker dev-oidc clean

all: web server agent

web:
	cd web && npm ci --no-audit --no-fund && npm run build

server:
	CGO_ENABLED=0 go build $(GOFLAGS_BUILD) -o bin/simpleremote-server ./server/cmd/simpleremote-server

# Builds the agent for the current OS (it uses cgo, so build each OS natively).
agent:
	go build $(GOFLAGS_BUILD) -o bin/simpleremote-agent ./agent/cmd/simpleremote-agent

test:
	go vet ./...
	go test -race ./server/... ./internal/...
	go test ./agent/...

test-web:
	cd web && npm run check && npm test

# Needs an X display (e.g. `Xvfb :99 &` and DISPLAY=:99) and xdotool.
e2e:
	go build -tags e2e -o bin/agent-e2e ./agent/cmd/simpleremote-agent
	E2E_AGENT_BIN=$(CURDIR)/bin/agent-e2e go test -tags e2e -count=1 -v ./server/e2e/

docker:
	docker build -f deploy/Dockerfile --build-arg VERSION=$(VERSION) -t simpleremote:$(VERSION) .

# A throwaway OIDC provider on :9999 for local development.
dev-oidc:
	go run ./tools/mockoidc -issuer http://localhost:9999

clean:
	rm -rf bin web/dist/assets web/dist/index.html web/dist/favicon.svg
