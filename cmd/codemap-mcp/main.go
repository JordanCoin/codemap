package main

import (
	"os"

	"codemap/cmd"
)

func main() {
	if code := cmd.RunMCP(); code != 0 {
		os.Exit(code)
	}
}
