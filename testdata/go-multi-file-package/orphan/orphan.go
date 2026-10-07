package orphan

// Nothing in this module imports package orphan: its importer count is a
// true zero, not a dropped edge.
func Unused() int { return 0 }
