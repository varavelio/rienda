// Package filecomplete suggests the paths of a project that complete a mention.
//
// It joins the listing of a project with fuzzy ranking: List turns the entries
// of a Source into the suggestions of a completion, whose directories carry a
// trailing slash, and Rank narrows them to the ones that best match the query
// the user wrote. The caller keeps the suggestions between calls and lists the
// project again when it wants fresher ones, so the interface that drives the
// completion decides when the filesystem is read and never waits for it.
//
// The package depends on a Source for the listing instead of reading the
// filesystem itself, so the interface that drives it stays free of the
// filesystem: a caller injects the project on disk in production and a fixture
// in its tests.
package filecomplete
