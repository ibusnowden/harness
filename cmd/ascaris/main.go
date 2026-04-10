package main

import (
	"os"

	"ascaris/internal/cli"
)

func main() {
	cwd, err := os.Getwd()
	if err != nil {
		_, _ = os.Stderr.WriteString(err.Error() + "\n")
		os.Exit(1)
	}
	os.Exit(cli.Run(cli.Context{Root: cwd}, os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
