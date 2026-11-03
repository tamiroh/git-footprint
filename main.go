package main

import (
	"os"

	"github.com/tamiroh/git-footprint/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:]))
}
