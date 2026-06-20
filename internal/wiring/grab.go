package wiring

// grab.go is reserved for the grab bounded context wiring. The grab
// UC + evaluator + decision repos are currently wired inside
// BuildScan (catalog.go) because the scan UC depends on them and the
// pre-A-1 layout kept them in the same constructor. Future stories
// can split them out here once the catalog/grab boundary is fully
// drawn at the application layer.
