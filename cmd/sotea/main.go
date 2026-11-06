package main

import (
	"fmt"
	"os"

	"github.com/Spring-Ocean/gitea-cli-so/internal/cli"
)

// version can be set at build time using -ldflags.
var version = "dev"

func main() {
	if err := cli.NewRootCommand(version).Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
