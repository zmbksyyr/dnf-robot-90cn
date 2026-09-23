package webadmin

const restartHelperArgument = "--restart-helper"

func RestartHelperRequested(args []string) bool {
	return len(args) > 0 && args[0] == restartHelperArgument
}
