package pkg

// User is referenced by main.go through an import of this package, so a
// file-level edge does exist and an empty graph here would be a real finding.
type User struct {
	ID   string
	Name string
}
