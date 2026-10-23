package main

import (
	"fmt"
	"runtime/debug"
	"strings"
	"time"

	"github.com/maxsupermanhd/wrpl-inspector/v3/internal/terrainmap"
	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl"
	packetslot "github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/slot"
)

func validateACMIVersion(version string) error {
	if version != "2.1" && version != "2.2" {
		return fmt.Errorf("invalid -acmi-version %q: use 2.1 or 2.2", version)
	}
	return nil
}

func exporterVersion() string {
	version := "devel"
	if info, ok := debug.ReadBuildInfo(); ok {
		if info.Main.Version != "" && info.Main.Version != "(devel)" {
			version = info.Main.Version
		}
		for _, setting := range info.Settings {
			if setting.Key == "vcs.revision" {
				version += "+" + setting.Value
			}
			if setting.Key == "vcs.modified" && setting.Value == "true" {
				version += ".dirty"
			}
		}
	}
	return version
}

func headerText(raw []byte) string {
	if i := strings.IndexByte(string(raw), 0); i >= 0 {
		raw = raw[:i]
	}
	return strings.TrimSpace(string(raw))
}

func acmiMetadata(header wrpl.WRPLHeader, mapID string, layout terrainLayout, ctx acmiWriterContext) []string {
	place := mapID
	if place == "" {
		place = headerText(header.Raw_LocName[:])
	}
	if place == "" {
		place = headerText(header.Raw_Level[:])
	}
	if place == "" {
		place = "Unknown map"
	}
	mode := headerText(header.Raw_BattleType[:])
	if mode == "" {
		mode = headerText(header.Raw_BattleClass[:])
	}
	if mode == "" {
		mode = "Unknown mode"
	}
	stamp := "Unknown replay time"
	if header.StartTime > 0 {
		stamp = time.Unix(int64(header.StartTime), 0).UTC().Format(time.RFC3339)
	}
	created := ctx.recordingTime
	if created.IsZero() {
		created = time.Now().UTC()
	}
	comments := fmt.Sprintf("Exporter wrpl-acmi %s; -terrain-layout=%s; georeference commit=%s; altitude=game height; countermeasures=recorded sync or labelled drag/gravity estimates; radar locks=replay designations with geometric range/angles", exporterVersion(), layout, terrainmap.GeographicSourceCommit())
	comments += "; countermeasure identity=reviewed owner ammunition only; unresolved type defaults to Chaff (fallback), no type inference"
	comments += "; moving ground heading=path derivative when available, not hull/turret attitude"
	if ctx.eventCategories != "" {
		comments += "; message timeline=" + ctx.eventCategories + "; kill attribution=explicit identity and weapon, confirmed death time only"
	}
	if ctx.estimateTelemetry {
		comments += "; aircraft telemetry=position/attitude derived estimates, NOT game telemetry; zero wind; ISA atmosphere; IAS=EAS approximation; body G=specific force including gravity compensation"
	} else {
		comments += "; aircraft telemetry estimates=off"
	}
	if len(ctx.airfields) > 0 {
		comments += "; airfields=source mission/prefab runway geometry; conditional variants validated by three replay radars; elevation=replay radar or labelled terrain/nominal approximation; runway direction is geometric, not an active landing clearance"
	}
	return []string{
		"Title=" + escapeACMIValue(place+" / "+mode+" / "+stamp),
		"Author=" + escapeACMIValue(*recordingAuthor),
		"RecordingTime=" + created.UTC().Format(time.RFC3339),
		"Category=War Thunder", "DataRecorder=wrpl-acmi",
		"Comments=" + escapeACMIValue(comments),
	}
}

// Keep database-only names and aircraft registration numbers out of telemetry.
func presentationProperties(tr *track, coalition string) []string {
	props := []string{"Group=" + escapeACMIValue("War Thunder "+coalition)}
	if tr.groundModel != nil {
		switch groundModelKey(tr.groundModel.RawModel) {
		case "unit_airfield_flight_radar_char", "unit_airfield_main_radar_char":
			props = append(props, "Importance=0.75")
		}
	}
	return props
}

// Pipes delimit object ids and the event message, and cannot occur inside text.
func bookmarkLine(ids []string, text string) string {
	text = strings.NewReplacer("|", "/", "\r", " ", "\n", " ").Replace(text)
	parts := []string{"0,Event=Bookmark"}
	for _, id := range ids {
		if id != "" {
			parts = append(parts, id)
		}
	}
	return strings.Join(append(parts, escapeACMIValue(text)), "|")
}

func acmiBookmarks(tracks []*track, events []acmiEvent, radar *radarLockExport, ids map[trackKey]string, slot *packetslot.PacketSlotParser, names map[string]string) map[uint32][]string {
	out := map[uint32][]string{}
	seen := map[string]bool{}
	add := func(t uint32, line string) {
		key := fmt.Sprintf("%d:%s", t, line)
		if !seen[key] {
			out[t] = append(out[t], line)
			seen[key] = true
		}
	}
	for _, tr := range tracks {
		if tr == nil || len(tr.samples) == 0 {
			continue
		}
		id := ids[tr.key]
		name := trackName(tr, slot, names)
		if tr.key.air && tr.hasEnd {
			add(tr.end, bookmarkLine([]string{id}, "Destroyed: "+name))
		}
		if !tr.key.missile && !tr.key.bomb {
			continue
		}
		t := tr.samples[0].t
		text := "Missile first observed: " + name
		if tr.key.bomb {
			text = "Bomb first observed: " + name
		}
		if tr.projectileSource != nil {
			t = tr.projectileSource.SpawnSample.Time
			text = "Missile launch: " + name
			if tr.key.bomb {
				text = "Bomb release: " + name
			}
		}
		// A construction can predate the first synchronized weapon position.
		// Keep its true release time without referencing an unborn ACMI object.
		var objectIDs []string
		if t >= tr.samples[0].t && (!tr.hasEnd || t <= tr.end) {
			objectIDs = append(objectIDs, id)
		}
		if tr.parent != nil && len(tr.parent.samples) > 0 && t >= tr.parent.samples[0].t && (!tr.parent.hasEnd || t <= tr.parent.end) {
			objectIDs = append(objectIDs, ids[tr.parent.key])
		}
		add(t, bookmarkLine(objectIDs, text))
	}
	for _, event := range events {
		var objectIDs []string
		if event.hasParent {
			objectIDs = append(objectIDs, ids[trackKey{air: true, id: event.parentID}])
		}
		add(event.t, bookmarkLine(objectIDs, event.text))
	}
	if radar != nil {
		for _, row := range radar.logRows {
			objectIDs := []string{radar.ids[row.source]}
			text := "Radar designation cleared"
			if len(row.uids) > 0 {
				text = "Radar target designation"
				for _, uid := range row.uids {
					objectIDs = append(objectIDs, radar.ids[uid])
				}
			}
			add(row.t, bookmarkLine(objectIDs, text))
		}
	}
	return out
}
