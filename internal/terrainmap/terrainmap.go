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

// These are the 68 air maps that exist in the task's terrain catalog.
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
stalingrad stalingrad_w tunisia wake_island zhengzhou`)

var (
	known            = make(map[string]struct{}, len(knownIDs))
	mapIDPrefix      = "WarThunder."
	atlasMapIDPrefix = "WarThunder.Atlas1."
	filenameBreaks   = "_-./ ()+[]"
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

// NormalizeMapID accepts a canonical id with no prefix, the legacy
// WarThunder. prefix, or the Atlas V1 WarThunder.Atlas1. prefix. It normalizes
// case, but rejects paths, whitespace, and other forms that could silently
// select the wrong map.
func NormalizeMapID(s string) (string, error) {
	s = strings.TrimSpace(s)
	switch lower := strings.ToLower(s); {
	case strings.HasPrefix(lower, strings.ToLower(atlasMapIDPrefix)):
		s = s[len(atlasMapIDPrefix):]
	case strings.HasPrefix(lower, strings.ToLower(mapIDPrefix)):
		s = s[len(mapIDPrefix):]
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

func FormatMapID(id string) string { return mapIDPrefix + id }

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

// FromFilename finds one exact catalog id as a filename token. It never uses
// coordinates, weather words, or fuzzy names. A shorter id is ignored only
// when its whole match is another id's prefix, so mozdok does not make
// mozdok_winter ambiguous. Separate map tokens always make the name ambiguous.
func FromFilename(filename string) (string, bool) {
	base := strings.ToLower(path.Base(strings.ReplaceAll(filename, `\`, "/")))
	if strings.HasSuffix(base, ".acmi") {
		base = strings.TrimSuffix(base, ".acmi")
	}
	type match struct {
		id         string
		start, end int
	}
	var matches []match
	for _, id := range knownIDs {
		for from := 0; from <= len(base)-len(id); from++ {
			if !strings.HasPrefix(base[from:], id) {
				continue
			}
			end := from + len(id)
			if (from > 0 && !strings.ContainsRune(filenameBreaks, rune(base[from-1]))) ||
				(end < len(base) && !strings.ContainsRune(filenameBreaks, rune(base[end]))) {
				continue
			}
			matches = append(matches, match{id: id, start: from, end: end})
		}
	}
	ids := map[string]struct{}{}
	for i, short := range matches {
		contained := false
		for j, long := range matches {
			if i != j && short.id != long.id &&
				long.start <= short.start && long.end >= short.end {
				contained = true
				break
			}
		}
		if !contained {
			ids[short.id] = struct{}{}
		}
	}
	if len(ids) != 1 {
		return "", false
	}
	for id := range ids {
		return id, true
	}
	return "", false
}
