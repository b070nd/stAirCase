// export_test.go — compiled only during `go test`.
// Exposes unexported engine functions to the black-box test package.
package engine

// MatchGlob exposes matchGlob for unit testing.
var MatchGlob = matchGlob
