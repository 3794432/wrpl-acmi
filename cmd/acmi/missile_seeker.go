package main

import (
	"strconv"

	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/projectiles"
)

// Display abbreviations do not alter recorded state names or native modes.
func missileSeekerDisplayName(s acmiSample) string {
	if state, ok := missileSeekerStateForSample(s); ok {
		switch state.Mode {
		case projectiles.SeekerModeSearch:
			return "SRC"
		case projectiles.SeekerModeTrack:
			return "TRK"
		}
	}
	return "N/A"
}

// missileSeekerProperties maps only validated in-flight WeaponSync seeker
// evidence. Every property is emitted each frame so a missing or unknown block
// clears values from the previous frame. A target object and body-relative
// angles are intentionally omitted because WeaponSync carries neither.
func missileSeekerProperties(tr *track, s acmiSample) []string {
	values := [14]string{
		"WT_SeekerKind=",
		"WT_SeekerState=",
		"WT_SeekerSource=",
		"WT_SeekerBits=",
		"WT_SeekerLOSX=",
		"WT_SeekerLOSY=",
		"WT_SeekerLOSZ=",
		"WT_SeekerTargetEstimateX=",
		"WT_SeekerTargetEstimateY=",
		"WT_SeekerTargetEstimateZ=",
		"WT_SeekerRange=",
		"LockedTargetMode=0",
		"LockedTargetRange=",
		"RadarMode=0",
	}

	state, ok := missileSeekerStateForSample(s)
	if !ok {
		return values[:14]
	}
	values[0] = "WT_SeekerKind=" + string(state.Kind)
	values[1] = "WT_SeekerState=" + string(state.Mode)
	values[2] = "WT_SeekerSource=WeaponSync"
	values[3] = "WT_SeekerBits=" + strconv.FormatUint(uint64(state.BitLength), 10)
	if state.HasLineOfSight {
		for i, value := range state.LineOfSight {
			values[4+i] = "WT_SeekerLOS" + string(rune('X'+i)) + "=" + strconv.FormatFloat(float64(value), 'f', 6, 32)
		}
	}
	if state.HasTargetEstimate {
		for i, value := range state.TargetEstimate {
			values[7+i] = "WT_SeekerTargetEstimate" + string(rune('X'+i)) + "=" + strconv.FormatFloat(float64(value), 'g', -1, 32)
		}
	}
	if state.HasRange {
		values[10] = "WT_SeekerRange=" + strconv.FormatUint(uint64(state.RangeMeters), 10)
	}

	if state.Kind == projectiles.SeekerKindRadar {
		if state.Mode == projectiles.SeekerModeTrack {
			values[11] = "LockedTargetMode=1"
			if state.HasRange && state.RangeMeters > 0 {
				values[12] = "LockedTargetRange=" + strconv.FormatUint(uint64(state.RangeMeters), 10)
			}
		}
		if missileSeekerExplicitARH(tr) {
			switch state.Mode {
			case projectiles.SeekerModeSearch, projectiles.SeekerModeTrack:
				values[13] = "RadarMode=1"
			}
		}
	}
	return values[:14]
}

func missileSeekerStateForSample(s acmiSample) (projectiles.SeekerState, bool) {
	if s.projectile == nil || !s.projectile.FromSync || s.projectile.RawMotion == nil {
		return projectiles.SeekerState{}, false
	}
	return s.projectile.RawMotion.SeekerState()
}

// missileSeekerExplicitARH accepts only a high-confidence ARH table result or
// the explicit arh_shell template token. Inferred guidance alone cannot
// activate the radar, but must not erase independent explicit template evidence.
// Conflicting or SARH/IR results always block activation.
func missileSeekerExplicitARH(tr *track) bool {
	if tr == nil || !tr.key.missile {
		return false
	}
	if info := tr.missileGuidance; info != nil {
		if info.Type == "SARH" || info.Type == "IR" || info.Confidence == "conflict" {
			return false
		}
		if info.Type == "ARH" && (info.Confidence == "confirmed" || info.Confidence == "high") {
			return true
		}
	}
	if tr.projectileSource != nil {
		hints := missileTemplateGuidanceHints(tr.projectileSource.Template)
		if len(hints) == 1 && hints[0] == "ARH" {
			return true
		}
	}
	return false
}
