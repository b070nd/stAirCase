// export_test.go — compiled only during `go test`.
// Exposes unexported helper functions to the black-box test package
// (package template_test).
package template

// PyStr wraps pyStr for black-box tests.
var PyStr = pyStr

// PyIdent wraps pyIdent for black-box tests.
var PyIdent = pyIdent

// BuildConditionalGroups wraps buildConditionalGroups for black-box tests.
var BuildConditionalGroups = buildConditionalGroups
