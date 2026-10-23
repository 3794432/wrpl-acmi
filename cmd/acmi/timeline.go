package main

import (
	"fmt"
	"sort"
	"strings"

	packetslot "github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/slot"
)

const (
	defaultTimelineEvents = "kill,chat,join,damage,radar"
	allTimelineEvents     = "kill,chat,join,damage,award,radar"
)

type timelineEvent struct {
	t    uint32
	keys []trackKey
	text string
}

func parseTimelineEvents(spec string) (map[string]bool, error) {
	out := map[string]bool{}
	spec = strings.ToLower(strings.TrimSpace(spec))
	if spec == "" || spec == "none" || spec == "off" {
		return out, nil
	}
	if spec == "all" {
		spec = allTimelineEvents
	}
	for _, name := range strings.Split(spec, ",") {
		name = strings.TrimSpace(name)
		switch name {
		case "kill", "chat", "join", "damage", "award", "radar":
			out[name] = true
		default:
			return nil, fmt.Errorf("invalid -events category %q: use %s, all, or none", name, allTimelineEvents)
		}
	}
	return out, nil
}

// Events never revive an unborn/deleted object. Unattributed chat/roster
// messages remain objectless; attributed messages require all their objects.
func timelineLines(events []timelineEvent, tracks []*track, ids map[trackKey]string) map[uint32][]string {
	byKey := map[trackKey]*track{}
	for _, tr := range tracks {
		if tr != nil {
			byKey[tr.key] = tr
		}
	}
	sorted := append([]timelineEvent(nil), events...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].t != sorted[j].t {
			return sorted[i].t < sorted[j].t
		}
		return sorted[i].text < sorted[j].text
	})
	out := map[uint32][]string{}
	seen := map[string]bool{}
	for _, event := range sorted {
		parts := []string{"0,Event=Message"}
		valid := true
		for _, key := range event.keys {
			tr := byKey[key]
			if tr == nil || ids[key] == "" || len(tr.samples) == 0 || event.t < tr.samples[0].t || (tr.hasEnd && event.t > tr.end) {
				valid = false
				break
			}
			parts = append(parts, ids[key])
		}
		text := strings.NewReplacer("|", "/", "\r", " ", "\n", " ").Replace(event.text)
		if !valid || strings.TrimSpace(text) == "" {
			continue
		}
		line := strings.Join(append(parts, escapeACMIValue(text)), "|")
		dedup := fmt.Sprintf("%d:%s", event.t, line)
		if !seen[dedup] {
			out[event.t] = append(out[event.t], line)
			seen[dedup] = true
		}
	}
	return out
}

// These are replay target designations, not evidence of a radar STT mode.
// Reuse prepared rows so changes between source FM frames are retained too.
func radarTimeline(x *radarLockExport, slot *packetslot.PacketSlotParser, names map[string]string) []timelineEvent {
	if x == nil {
		return nil
	}
	out := make([]timelineEvent, 0, len(x.logRows))
	for _, row := range x.logRows {
		source := x.tracks[row.source]
		if source == nil {
			continue
		}
		event := timelineEvent{t: row.t, keys: []trackKey{source.key}, text: "Radar designation cleared"}
		if len(row.uids) > 0 {
			var targets []string
			for _, uid := range row.uids {
				if target := x.tracks[uid]; target != nil {
					targets = append(targets, trackName(target, slot, names))
				}
			}
			if len(targets) == 0 {
				continue
			}
			event.text = "Radar target designation: " + strings.Join(targets, "; ")
			if row.hasRng {
				event.text += fmt.Sprintf("; primary range %.0f m (geometric)", row.rangeM)
			}
		}
		out = append(out, event)
	}
	return out
}
