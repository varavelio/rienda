// Package filecomplete suggests the paths of a project that complete a mention.
//
// It joins the listing of a project with fuzzy ranking: the entries of the
// Source are read once, turned into suggestions whose directories carry a
// trailing slash, and ranked against the query the user wrote. The result is
// the list a completion popup shows while the query narrows.
//
// The package depends on a Source for the listing instead of reading the
// filesystem itself, so the interface that drives it stays free of the
// filesystem: a caller injects the project on disk in production and a fixture
// in its tests.
package filecomplete
