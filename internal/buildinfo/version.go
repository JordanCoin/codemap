package buildinfo

import "strings"

var version = "dev"

func Current() string {
	if version == "" {
		return "dev"
	}
	return version
}

func Equal(a, b string) bool {
	return strings.TrimPrefix(a, "v") == strings.TrimPrefix(b, "v")
}
