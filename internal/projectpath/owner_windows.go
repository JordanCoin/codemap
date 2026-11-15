//go:build windows

package projectpath

import "os"

// The user profile directory is already private to the user.
func ownedByCurrentUser(os.FileInfo) bool { return true }
