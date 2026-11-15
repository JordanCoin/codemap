//go:build windows

package projectpath

import "os"

// The per-user cache under %LocalAppData% is already private to the user.
func ownedByCurrentUser(os.FileInfo) bool { return true }
