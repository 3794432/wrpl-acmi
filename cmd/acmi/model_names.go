package main

import (
	_ "embed"
	"encoding/json"
	"strings"
)

// English vehicle labels extracted from the installed game's lang/units.csv.
// Embedding keeps the standalone exporter independent of the game installation.
//
//go:embed model_names.json
var modelNameData []byte

var modelDisplayNames = func() map[string]string {
	var names map[string]string
	if err := json.Unmarshal(modelNameData, &names); err != nil {
		panic("invalid embedded vehicle names: " + err.Error())
	}
	return names
}()

func displayModelName(className string) string {
	className = strings.TrimSpace(className)
	if name := modelDisplayNames[className]; name != "" {
		return name
	}
	// Older aircraft use hyphens in replay class IDs but underscores in
	// localization keys, for example f-86f-2 -> f_86f_2.
	if name := modelDisplayNames[strings.ReplaceAll(className, "-", "_")]; name != "" {
		return name
	}
	return className
}
