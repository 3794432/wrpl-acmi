package main

// aircraftTrackAt resolves a replay UID within its recorded lifetime. Multiple
// simultaneous matches are ambiguous and must never be resolved by list order.
func aircraftTrackAt(tracks []*track, uid uint64, t uint32) *track {
	var found *track
	for _, tr := range tracks {
		if !isKillAircraft(tr) || tr.key.id != uid || !killTrackContains(tr, t) {
			continue
		}
		if found != nil {
			return nil
		}
		found = tr
	}
	return found
}

// A store may be recorded before its owner's first position. Preserve an
// explicit, globally unique owner in that case; recycled UIDs require a live
// sortie and must never fall back to the first or last occurrence.
func aircraftParentAt(tracks []*track, uid uint64, t uint32) *track {
	if tr := aircraftTrackAt(tracks, uid, t); tr != nil {
		return tr
	}
	var only *track
	for _, tr := range tracks {
		if !isKillAircraft(tr) || tr.key.id != uid {
			continue
		}
		if only != nil {
			return nil
		}
		only = tr
	}
	if only != nil && len(only.samples) > 0 && t < only.samples[0].t {
		return only
	}
	return nil
}

func projectileParentTime(sourceTime uint32, samples []acmiSample) uint32 {
	if sourceTime != 0 {
		return sourceTime
	}
	if len(samples) > 0 {
		return samples[0].t
	}
	return 0
}
