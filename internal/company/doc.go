// Package company reads the files of an installed company library: the
// company.toml of a version directory, the recipe files of a recipes library,
// and the tree hash that detects a change to a reference copy
// (docs/design/companies.md, docs/design/company-recipe-schema.md).
//
// It is pure. It reads bytes and directories it is handed, runs no command,
// dials nothing and holds no state, and nothing in marvel calls it yet. A
// refusal is a *Refusal that carries the row it came from and the message the
// spec's refusal table fixes; every field a message prints goes through one
// quoting function.
package company
