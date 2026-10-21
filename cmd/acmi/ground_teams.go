package main

import (
	"strconv"
	"strings"

	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/groundmeta"
)

// Team is joined by the actual replay ground UID, independently of model
// naming. As for aircraft, replay team 1 is Blue and team 2 is Red.
func applyGroundTeamEvidence(tracks []*track, models map[uint64]groundmeta.Info) int {
	assigned := 0
	for _, tr := range tracks {
		uid, ok := groundTrackUID(tr)
		if !ok {
			continue
		}
		info, ok := models[uid]
		if !ok || info.Conflict || info.Team == nil || strings.TrimSpace(info.TeamSource) == "" {
			continue
		}
		if *info.Team != 1 && *info.Team != 2 {
			continue
		}
		tr.groundTeam = *info.Team
		tr.groundTeamSource = info.TeamSource
		assigned++
	}
	return assigned
}

func groundTeamProperties(tr *track) []string {
	if !isGroundTrack(tr) || tr.groundTeamSource == "" || (tr.groundTeam != 1 && tr.groundTeam != 2) {
		return nil
	}
	return []string{
		"WT_GroundTeam=" + strconv.FormatInt(int64(tr.groundTeam), 10),
		"WT_GroundTeamSource=" + escapeACMIValue(tr.groundTeamSource),
	}
}
