package main

import (
	"os"

	"agent-terminal/internal/cmd"
)

var (
	version = "dev"
)

func main() {
	root := cmd.NewCmdRoot(version)

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}
