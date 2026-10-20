package main

import "strconv"

type missileINSReading struct {
	valid   bool
	raw     float32
	seconds float64
}

func missileINSReadingForSample(s acmiSample) missileINSReading {
	if s.projectile == nil || !s.projectile.FromSync || s.projectile.RawMotion == nil {
		return missileINSReading{}
	}
	raw, ok := s.projectile.RawMotion.SeekerValue()
	if !ok {
		return missileINSReading{}
	}
	// The wire float is already in seconds. The 48 Hz source name describes
	// its quantization frequency; converting the value to ticks would be wrong.
	return missileINSReading{valid: true, raw: raw, seconds: float64(raw)}
}

func (r missileINSReading) seekerTimerValue() string {
	if !r.valid {
		return ""
	}
	return strconv.FormatFloat(float64(r.raw), 'g', -1, 32)
}

func (r missileINSReading) inertialTimeValue() string {
	if !r.valid {
		return ""
	}
	return strconv.FormatFloat(r.seconds, 'f', 6, 64)
}

func (r missileINSReading) validValue() string {
	if r.valid {
		return "1"
	}
	return "0"
}
