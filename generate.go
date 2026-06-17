// Package gostashbox is a Go client/library for stash-box's GraphQL API
// (StashDB and any other instance).
//
// This file carries the code generation pipeline. genops compiles the vendored
// SDL into genqlient operations and fragments, the operation manifest, and the
// catalog; genqlient then turns the operations and fragments into the typed
// stash-box client. The two directives run in order, genops first.
package gostashbox

//go:generate go run ./internal/gen
//go:generate go run github.com/Khan/genqlient
