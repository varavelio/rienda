// Package credentials stores the provider credentials of a Rienda user.
//
// The credentials live at "~/.rienda/credentials.json" as one JSON object
// mapping provider names to their credential fields. Only the api_key field
// is read today; unknown fields (future OAuth tokens and friends) survive
// every rewrite untouched. The store is read by the harness to authenticate
// provider requests and written by the auth interface of the product.
//
// Every write follows a fresh read of the file, so two concurrent writers —
// two Rienda instances, or the auth interface of another terminal — cannot
// lose each other's entries beyond a millisecond-scale window. The write
// itself replaces the file atomically. A malformed file degrades to "every
// provider unauthenticated": credentials are never a reason to refuse a
// session.
package credentials
