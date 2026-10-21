// Package terrainmap resolves the canonical Tacview MapId used by the
// War Thunder terrain catalog shipped with this project.
package terrainmap

import (
	"errors"
	"fmt"
	"path"
	"strings"
)

var ErrUnknown = errors.New("unknown War Thunder map")

// These are the 69 air maps that exist in the task's terrain catalog.
var knownIDs = strings.Fields(`
air_afghan air_africa_desert air_archipelago air_denmark
air_equatorial_island air_falklands air_grand_canyon air_israel air_kamchatka
air_ladoga air_mysterious_valley air_normandy air_pyrenees
air_race_phiphi_islands air_skyscraper_city air_smolensk
air_south_eastern_city air_southeastern_cliffs air_vietnam
arcade_africa_canyon arcade_africa_seashore arcade_alps
arcade_asia_4roads arcade_canyon_snow arcade_ireland arcade_mediterranean
arcade_norway_fjords arcade_norway_green arcade_norway_plain
arcade_phiphi_crater arcade_phiphi_crater_rocks arcade_rice_terraces
arcade_snow_rocks arcade_tabletop_mountain arcade_zhang_park berlin britain
bulge caribbean_islands dover_strait guadalcanal guam honolulu hurtgen iwo_jima
khalkhin_gol korea korsun krymsk kursk malta midway moscow mozdok mozdok_winter
norway peleliu port_moresby ruhr saipan sector_montmedy sicily spain
stalingrad stalingrad_w tunisia wake_island zhengzhou
avg_egypt_sinai`)

var (
	known            = make(map[string]struct{}, len(knownIDs))
	atlasMapIDPrefix = "WarThunder.Atlas1."
)

func init() {
	for _, id := range knownIDs {
		known[id] = struct{}{}
	}
}

func KnownIDs() []string { return append([]string(nil), knownIDs...) }

func IsKnown(id string) bool {
	_, ok := known[id]
	return ok
}

// NormalizeMapID accepts a canonical id with no prefix, the current Geographic
// V1 WarThunder.Geo1. prefix, or the current Atlas V1 WarThunder.Atlas1.
// prefix. It normalizes case, but rejects the legacy WarThunder. prefix, paths,
// whitespace, and other forms that could silently select the wrong map.
func NormalizeMapID(s string) (string, error) {
	s = strings.TrimSpace(s)
	switch lower := strings.ToLower(s); {
	case strings.HasPrefix(lower, strings.ToLower(atlasMapIDPrefix)):
		s = s[len(atlasMapIDPrefix):]
	case strings.HasPrefix(lower, strings.ToLower(geographicMapIDPrefix)):
		s = s[len(geographicMapIDPrefix):]
	}
	s = strings.ToLower(s)
	if s == "" || !IsKnown(s) {
		return "", fmt.Errorf("%w: %q", ErrUnknown, s)
	}
	for _, r := range s {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '_' {
			return "", fmt.Errorf("%w: unsafe map id %q", ErrUnknown, s)
		}
	}
	return s, nil
}

func FormatAtlasMapID(id string) string { return atlasMapIDPrefix + id }

// HeaderString extracts a NUL-padded replay header string without changing
// non-NUL interior bytes.
func HeaderString(raw []byte) string {
	return strings.Trim(string(raw), "\x00 \t\r\n")
}

// FromLevel accepts a compiled level path or its bare canonical basename.
func FromLevel(value string) (string, bool) {
	value = strings.TrimSpace(strings.ReplaceAll(value, `\`, "/"))
	base := strings.ToLower(path.Base(value))
	base = strings.TrimSuffix(base, ".blkx")
	base = strings.TrimSuffix(base, ".bin")
	return base, IsKnown(base)
}

// FromLocation accepts the stable location suffix found in replay locName,
// for example missions/_BfD_norespawn;location/air_grand_canyon.
func FromLocation(value string) (string, bool) {
	value = strings.ToLower(strings.TrimSpace(strings.ReplaceAll(value, `\`, "/")))
	if i := strings.LastIndex(value, "location/"); i >= 0 {
		value = value[i+len("location/"):]
		value = strings.Trim(value, "/")
	}
	return value, IsKnown(value)
}

// ResolveReplay resolves a replay header from its real level and location
// fields. Conflicting known values are errors instead of guesses.
func ResolveReplay(level, locName string) (string, error) {
	levelID, levelOK := FromLevel(level)
	locID, locOK := FromLocation(locName)
	if levelOK && locOK && levelID != locID {
		return "", fmt.Errorf("conflicting replay map fields: level=%q locName=%q", levelID, locID)
	}
	if levelOK {
		return levelID, nil
	}
	if locOK {
		return locID, nil
	}
	return "", fmt.Errorf("%w: level=%q locName=%q", ErrUnknown, level, locName)
}
