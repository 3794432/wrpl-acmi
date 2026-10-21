package main

import (
	_ "embed"
	"encoding/json"
	"math"
	"sort"
	"strconv"
	"strings"
)

//go:embed ground_threats.json
var groundThreatData []byte

type groundThreatEnvelope struct {
	Kind          string  `json:"kind"`
	Range         float64 `json:"range_m"`
	Source        string  `json:"source"`
	Basis         string  `json:"basis"`
	FieldPath     string  `json:"field_path"`
	ReferenceMode string  `json:"reference_mode,omitempty"`
}

type groundThreatProfile struct {
	Classification       string                 `json:"classification"`
	ClassificationSource string                 `json:"classification_source"`
	Envelopes            []groundThreatEnvelope `json:"envelopes"`
	MissingReason        string                 `json:"missing_reason"`
	RadarState           string                 `json:"radar_state"`
	RadarSource          string                 `json:"radar_source"`
}

type groundThreatCatalog struct {
	Schema int                            `json:"schema"`
	Units  map[string]groundThreatProfile `json:"units"`
}

var groundThreats = func() map[string]groundThreatProfile {
	var catalog groundThreatCatalog
	if err := json.Unmarshal(groundThreatData, &catalog); err != nil {
		panic("invalid embedded ground threat catalog: " + err.Error())
	}
	if catalog.Schema != 1 || catalog.Units == nil {
		panic("unsupported embedded ground threat catalog")
	}
	return catalog.Units
}()

func isAirDefenceProfile(p groundThreatProfile) bool {
	if strings.TrimSpace(p.ClassificationSource) == "" {
		return false
	}
	switch strings.ToLower(p.Classification) {
	case "spaa", "spaam", "air_defence":
		return true
	}
	return false
}

func validThreatEnvelope(e groundThreatEnvelope) bool {
	return (e.ReferenceMode == "" || e.ReferenceMode == "StaticModelReference" || e.ReferenceMode == "StaticAimLimitReference") &&
		(e.Kind == "gun" || e.Kind == "missile") && strings.TrimSpace(e.Source) != "" &&
		e.Range > 0 && e.Range <= 1e6 && !math.IsNaN(e.Range) && !math.IsInf(e.Range, 0)
}

func modelThreatEnvelopes(raw string) []groundThreatEnvelope {
	key := groundModelKey(raw)
	if profile, ok := groundThreats[strings.ToLower(key)]; ok && isAirDefenceProfile(profile) {
		// Use at most one envelope per weapon class. Each is the largest
		// sourced configuration reference, not evidence of the replay loadout.
		byKind := map[string]groundThreatEnvelope{}
		for _, e := range profile.Envelopes {
			if validThreatEnvelope(e) && e.Range > byKind[e.Kind].Range {
				byKind[e.Kind] = e
			}
		}
		// Hide only sourced, radarless gun-only systems. A missile envelope
		// keeps the system visible even when its guidance does not use radar.
		// Unknown radar equipment must not be treated as confirmed absence.
		if _, hasMissile := byKind["missile"]; !hasMissile &&
			profile.RadarState == "absent" && strings.TrimSpace(profile.RadarSource) != "" {
			return nil
		}
		out := make([]groundThreatEnvelope, 0, len(byKind))
		for _, e := range byKind {
			out = append(out, e)
		}
		sort.Slice(out, func(i, j int) bool {
			if out[i].Range != out[j].Range {
				return out[i].Range > out[j].Range
			}
			return out[i].Kind < out[j].Kind
		})
		return out
	}
	// Keep the two previously shipped, independently sourced profiles usable
	// when reading an older catalog. Do not spread this fallback by category.
	label := groundModelNames[key]
	e := groundThreatEnvelope{Kind: "gun", Range: label.EngagementRange, Source: label.EngagementRangeSource}
	if label.Kind == "anti_aircraft" && validThreatEnvelope(e) {
		return []groundThreatEnvelope{e}
	}
	return nil
}

func groundThreatProperties(tr *track) []string {
	if !isGroundTrack(tr) || tr.groundModel == nil {
		return nil
	}
	var props []string
	for i, e := range modelThreatEnvelopes(tr.groundModel.RawModel) {
		suffix := ""
		if i > 0 {
			suffix = strconv.Itoa(i + 1)
		}
		mode := e.ReferenceMode
		if mode == "" {
			mode = "StaticModelReference"
		}
		props = append(props,
			"EngagementRange"+suffix+"="+strconv.FormatFloat(e.Range, 'f', -1, 64),
			"EngagementMode"+suffix+"=1",
			"WT_EngagementRangeSource"+suffix+"="+escapeACMIValue(e.Source),
			"WT_EngagementRangeMode"+suffix+"="+mode,
			"WT_EngagementRangeKind"+suffix+"="+e.Kind,
		)
		if e.Basis != "" {
			props = append(props, "WT_EngagementRangeBasis"+suffix+"="+escapeACMIValue(e.Basis))
		}
	}
	return props
}
