//go:build !windows

package webadmin

import "fmt"

func runRestartHelper([]string) error {
	return fmt.Errorf("restart helper mode is only supported on windows")
}

func RunRestartHelper(args []string) error {
	return runRestartHelper(args)
}
