// Package data carries evidence-owned tables compiled into the inspector tools.
package data

import _ "embed"

// ecsHashesJSON is embedded so normal single-file builds retain ECS component
// names without requiring a data/ directory next to the executable.
//
//go:embed ecshashes.json
var ecsHashesJSON []byte

// ECSHashesJSON returns the embedded ECS component hash table as JSON. The
// returned slice is the compiled-in storage and must be treated as read-only.
func ECSHashesJSON() []byte {
	return ecsHashesJSON
}
