//go:build windows

package webadmin

func RunRestartHelper(args []string) error {
	return runRestartHelper(args)
}
