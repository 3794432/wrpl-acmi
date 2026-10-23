package main

import "math"

const (
	groundHeadingMaxGapMS         = uint32(2000)
	groundHeadingMinMovement      = 0.05
	groundHeadingMaxCarrySegments = 64
)

// groundPositionValue emits a geographic heading only when the ground path
// provides bounded horizontal movement evidence. The bearing is calculated
// after terrainPosition, so a reviewed georeference's axis orientation also
// applies to the heading. Stationary and incomplete paths retain the existing
// position-only T format rather than claiming an unobserved orientation.
func groundPositionValue(tr *track, index int) string {
	if tr == nil || index < 0 || index >= len(tr.samples) {
		return ""
	}
	sample := tr.samples[index]
	heading, ok := groundSampleHeading(tr, index)
	if !ok {
		return acmiBombPositionValue(sample)
	}
	// The source samples are shared with other exporters. Change only the
	// emitted copy: level terrain objects and use the horizontal path bearing.
	sample.roll = 0
	sample.pitch = 0
	sample.yaw = heading
	return acmiPositionValue(sample)
}

// groundHeadingProperties describes the path-derived heading attached to at
// least one sample. It deliberately does not claim a hull or turret heading.
func groundHeadingProperties(tr *track) []string {
	if !isGroundTrack(tr) || tr.groundStatic {
		return nil
	}
	for i := range tr.samples {
		if _, ok := groundSampleHeading(tr, i); ok {
			return []string{"WT_HeadingSource=PathDerivative"}
		}
	}
	return nil
}

func groundSampleHeading(tr *track, index int) (float64, bool) {
	if !isGroundTrack(tr) || tr.groundStatic || index < 0 || index >= len(tr.samples) {
		return 0, false
	}
	if !groundSampleInLifetime(tr, tr.samples[index]) {
		return 0, false
	}
	// Prefer the segment leaving this sample. At the end of a moving path, or
	// when a vehicle has just stopped, the arriving segment supplies the last
	// direction that was actually observed.
	if index+1 < len(tr.samples) {
		if heading, ok := groundBearing(tr, tr.samples[index], tr.samples[index+1]); ok {
			return heading, true
		}
	}
	if index > 0 {
		if heading, ok := groundBearing(tr, tr.samples[index-1], tr.samples[index]); ok {
			return heading, true
		}
	}
	// This narrow carry-forward keeps a brief GPS stop from erasing a measured
	// direction. It remains tied to the last observed travel segment and ends
	// after two seconds; it never invents turret or static-object orientation.
	return lastGroundHeading(tr, index)
}

func lastGroundHeading(tr *track, index int) (float64, bool) {
	current := tr.samples[index]
	if !groundSampleInLifetime(tr, current) {
		return 0, false
	}
	// Search only the segments before the immediate incoming segment, which
	// groundSampleHeading already tested. The fixed bound prevents a large run
	// of stopped samples from turning this into a whole-track scan.
	for end, checked := index-1, 0; end > 0 && checked < groundHeadingMaxCarrySegments; end, checked = end-1, checked+1 {
		from, to := tr.samples[end-1], tr.samples[end]
		if !groundSampleInLifetime(tr, from) || !groundSampleInLifetime(tr, to) || to.t >= current.t {
			break
		}
		// Carry may span a measured stop, but never a timestamp gap or a
		// discontinuity in sample chronology.
		if from.t >= to.t || to.t-from.t > groundHeadingMaxGapMS || current.t-to.t > groundHeadingMaxGapMS {
			break
		}
		if heading, ok := groundBearing(tr, from, to); ok {
			return heading, true
		}
	}
	return 0, false
}

func groundSampleInLifetime(tr *track, sample acmiSample) bool {
	return tr != nil && (!tr.hasEnd || sample.t <= tr.end)
}

func groundBearing(tr *track, from, to acmiSample) (float64, bool) {
	if !groundSampleInLifetime(tr, from) || !groundSampleInLifetime(tr, to) {
		return 0, false
	}
	dx, dz := to.x-from.x, to.z-from.z
	if math.Hypot(dx, dz) < groundHeadingMinMovement || to.t <= from.t || to.t-from.t > groundHeadingMaxGapMS {
		return 0, false
	}
	fromLat, fromLon := terrainPosition(from.x, from.z)
	toLat, toLon := terrainPosition(to.x, to.z)
	return geographicBearing(fromLat, fromLon, toLat, toLon)
}

// geographicBearing returns Tacview's clockwise-from-north heading.
func geographicBearing(fromLat, fromLon, toLat, toLon float64) (float64, bool) {
	for _, v := range []float64{fromLat, fromLon, toLat, toLon} {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return 0, false
		}
	}
	const degrees = math.Pi / 180
	phi1, phi2 := fromLat*degrees, toLat*degrees
	deltaLon := math.Mod(toLon-fromLon+180, 360) - 180
	deltaLambda := deltaLon * degrees
	north := math.Cos(phi1)*math.Sin(phi2) - math.Sin(phi1)*math.Cos(phi2)*math.Cos(deltaLambda)
	east := math.Sin(deltaLambda) * math.Cos(phi2)
	if math.Hypot(east, north) < 1e-15 {
		return 0, false
	}
	heading := math.Atan2(east, north) / degrees
	if heading < 0 {
		heading += 360
	}
	return heading, true
}
