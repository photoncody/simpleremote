//go:build e2e

package main

import "os"

// autoAllow lets automated end-to-end tests skip the consent prompt. This
// file is only compiled with -tags e2e; release builds never include it.
func autoAllow() bool { return os.Getenv("SIMPLEREMOTE_E2E_AUTO_ALLOW") == "1" }
