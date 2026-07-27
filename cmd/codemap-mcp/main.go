package main

import (
	"os"

	"codemap/cmd"
)

func main() {
	if code := cmd.RunMCP(os.Args[1:]); code != 0 {
		os.Exit(code)
	}
}
