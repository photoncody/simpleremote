//go:build !e2e

package main

// autoAllow is always false in normal builds: the user must click Allow.
func autoAllow() bool { return false }
