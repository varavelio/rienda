// Package fuzzy ranks items by how well their text matches a query.
//
// The ranking follows the fuzzy finders of editors such as Sublime Text,
// VS Code or IntelliJ IDEA: a query matches an item when its characters appear
// in the item text in the same order, and the match is scored by how close
// together those characters are and how well placed they are, so a match at
// the start of a word ranks above a scattered one. The comparison ignores
// case, which lets a lowercase query find a capitalized identifier.
//
// Search is generic over the item type and reads the text of an item through
// an accessor, so it ranks any kind of candidate: file paths, agent
// definitions, session titles or model names. The ranking implementation stays
// behind this interface, which gives the rest of the project a small and
// stable contract to depend on.
package fuzzy
