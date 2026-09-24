//go:build !windows

package main

func openRecoveryBrowser(string) error {
	return nil
}
