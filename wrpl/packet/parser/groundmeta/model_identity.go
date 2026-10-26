package groundmeta

import "strings"

// SameModelIdentity compares a class id with its explicitly qualified storage id.
func SameModelIdentity(a, b string) bool { return sameGroundModel(a, b) }

// The class component can contain a bare asset id while the storage contains
// the same id qualified by its unit directory. This is formatting, not a model
// conflict. Distinct explicit directories and arbitrary paths remain distinct.
func sameGroundModel(a, b string) bool {
	rootA, modelA := groundModelIdentity(a)
	rootB, modelB := groundModelIdentity(b)
	return modelA != "" && modelA == modelB && (rootA == rootB || rootA == "" || rootB == "")
}

func groundModelIdentity(raw string) (root, model string) {
	raw = strings.TrimSpace(strings.ReplaceAll(raw, "\\", "/"))
	for _, prefix := range []string{"tankModels/", "structures/", "ships/", "lightVehicles/", "wheeled_vehicles/"} {
		if strings.HasPrefix(raw, prefix) {
			model = strings.TrimPrefix(raw, prefix)
			if model != "" && !strings.Contains(model, "/") {
				return prefix, model
			}
		}
	}
	if strings.Contains(raw, "/") {
		return "opaque", raw
	}
	return "", raw
}
