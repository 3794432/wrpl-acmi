package main

import (
	_ "embed"
	"encoding/json"
	"strings"

	packetecs2 "github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/ecs2"
	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/groundmeta"
)

// Specific labels from War Thunder lang/units.csv; unlike generic _shop
// entries such as AAA, these identify the actual vehicle or gun model.
//
//go:embed ground_model_names.json
var groundModelNamesJSON []byte

type groundModelLabel struct {
	Name                  string  `json:"name,omitempty"`
	ACMIName              string  `json:"acmi_name,omitempty"`
	Kind                  string  `json:"kind,omitempty"`
	KindSource            string  `json:"kind_source,omitempty"`
	EngagementRange       float64 `json:"engagement_range_m,omitempty"`
	EngagementRangeSource string  `json:"engagement_range_source,omitempty"`
}

// Name drives Tacview's database matching; CallSign retains the game's label.
// Exact raw-model aliases prevent ItO 90M from matching a T-90 tank entry.
func groundACMIName(tr *track, displayName string) string {
	if !isGroundTrack(tr) || tr.groundModel == nil || displayName != tr.groundModel.DisplayName {
		return displayName
	}
	if label := groundModelNames[groundModelKey(tr.groundModel.RawModel)]; label.ACMIName != "" {
		return label.ACMIName
	}
	return displayName
}

var groundModelNames = func() map[string]groundModelLabel {
	var names map[string]groundModelLabel
	if err := json.Unmarshal(groundModelNamesJSON, &names); err != nil {
		panic(err)
	}
	return names
}()

func replayGroundModelEvidence(models map[uint64]groundmeta.Info) map[uint64]groundModelInfo {
	out := make(map[uint64]groundModelInfo, len(models))
	for uid, info := range models {
		if info.Conflict || info.ClassConflict || info.RawModel == "" {
			continue
		}
		kind := ""
		switch info.Storage {
		case "unit_storage__light_vehicle":
			kind = "vehicle"
		case "unit_storage__ship":
			kind = "ship"
		}
		// The tank storage format also carries unarmored anti-tank guns.
		// A mission role grouping tank/artillery/bunker cannot establish armor.
		if label := groundModelNames[groundModelKey(info.RawModel)]; label.Kind != "" && label.Kind != "armor" && label.KindSource != "" {
			kind = label.Kind
		}
		if profile, ok := groundThreats[strings.ToLower(groundModelKey(info.RawModel))]; ok && isAirDefenceProfile(profile) {
			kind = "anti_aircraft"
		}
		out[uid] = groundModelInfo{RawModel: info.RawModel, Source: info.Source, Kind: kind}
	}
	return out
}

func groundModelKey(raw string) string {
	parts := strings.Split(strings.ReplaceAll(strings.TrimSpace(raw), "\\", "/"), "/")
	return strings.TrimSuffix(parts[len(parts)-1], ".blk")
}

func groundDisplayModelName(raw string) string {
	key := groundModelKey(raw)
	if label := groundModelNames[key]; label.Name != "" {
		return label.Name
	}
	name := displayModelName(key)
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "aaa", "tank", "artillery", "pillbox", "static", "vehicle", "ground unit", "airfield", "bunker", "ship":
		return key
	}
	return name
}

// groundModelInfo is explicit replay identity evidence. Kind may refine the
// Tacview symbol only when the source metadata or vehicle assets establish it.
type groundModelInfo struct {
	RawModel    string
	DisplayName string
	Kind        string
	Source      string
}

func groundTrackUID(tr *track) (uint64, bool) {
	if !isGroundTrack(tr) {
		return 0, false
	}
	if tr.key.groundEntity && tr.key.id&groundUnitUIDNamespace != 0 {
		return tr.key.id &^ groundUnitUIDNamespace, true
	}
	if tr.entity != nil {
		if uid, ok := packetecs2.GetObjectData[int32](&tr.entity.Data, "uid"); ok && uid >= 0 {
			return uint64(uid), true
		}
	}
	return 0, false
}

func applyGroundModelEvidence(tracks []*track, models map[uint64]groundModelInfo) int {
	identified := 0
	for _, tr := range tracks {
		uid, ok := groundTrackUID(tr)
		if !ok {
			continue
		}
		info, ok := models[uid]
		info.RawModel = strings.TrimSpace(info.RawModel)
		info.Source = strings.TrimSpace(info.Source)
		if !ok || info.RawModel == "" || info.Source == "" || len(info.RawModel) > 512 ||
			strings.ContainsAny(info.RawModel, "\x00\r\n") || info.Kind == "aircraft" {
			continue
		}
		info.DisplayName = strings.TrimSpace(info.DisplayName)
		if info.DisplayName == "" {
			info.DisplayName = groundDisplayModelName(info.RawModel)
		}
		copy := info
		tr.groundModel = &copy
		identified++
	}
	return identified
}

func groundModelType(info *groundModelInfo) string {
	if info == nil {
		return ""
	}
	switch info.Kind {
	case "armor":
		return "Ground+Vehicle+Armor"
	case "anti_aircraft":
		return "Ground+Vehicle+AntiAircraft"
	case "building":
		return "Ground+Static+Building"
	case "ship":
		return "Sea+Watercraft"
	case "vehicle", "ground":
		return "Ground+Vehicle"
	}
	return ""
}

func groundModelProperties(tr *track) []string {
	if !isGroundTrack(tr) || tr.groundModel == nil {
		return nil
	}
	props := []string{
		"WT_GroundModel=" + escapeACMIValue(tr.groundModel.RawModel),
		"WT_GroundModelSource=" + escapeACMIValue(tr.groundModel.Source),
	}
	props = append(props, groundThreatProperties(tr)...)
	return props
}
