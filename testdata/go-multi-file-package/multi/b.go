package multi

// B is defined in the second file; helper is referenced from a.go without an
// import, which is the same-package reference codemap does not model.
func B() int { return 2 }

func helper() int { return 1 }
