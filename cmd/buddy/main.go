// buddy: the Buddy System CLI — multi-session claims, control, and messages.
package main

import (
	"os"

	"github.com/JsizzleR/buddy-system/internal/cli"
)

func main() {
	cwd, err := os.Getwd()
	if err != nil {
		os.Stderr.WriteString("buddy: " + err.Error() + "\n")
		os.Exit(1)
	}
	os.Exit(cli.Run(os.Args[1:], cli.Env{
		Stdin:  os.Stdin,
		Stdout: os.Stdout,
		Stderr: os.Stderr,
		Cwd:    cwd,
	}))
}
