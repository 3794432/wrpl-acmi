package projectiles

import (
	"math"

	ecsp "github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/ecs2"
)

// FlightField identifies the construction components that may carry Point3
// values relevant to a projectile. The hash table alone is not evidence that
// a field is present on any missile template.
type FlightField uint8

const (
	FlightVelocity FlightField = iota
	FlightPrevVelocity
	FlightOmega
	FlightPrevOmega
)

// Point3Evidence is a decoded construction value with an explicit presence
// flag. A zero vector with Present false means no value was decoded; it must
// not be interpreted as zero velocity or zero angular motion.
type Point3Evidence struct {
	Present bool
	X       float32
	Y       float32
	Z       float32
}

// FlightEvidence is optional construction evidence attached to a projectile.
// These are static component values, not a sampled velocity or angular-rate
// stream. Replication and integration remain the caller's responsibility.
type FlightEvidence struct {
	Velocity     Point3Evidence
	PrevVelocity Point3Evidence
	Omega        Point3Evidence
	PrevOmega    Point3Evidence
}

// FlightFieldFromComponentName maps only the four named construction data
// components. Unknown names are rejected so a similarly shaped component is
// never guessed into flight evidence.
func FlightFieldFromComponentName(name string) (FlightField, bool) {
	switch name {
	case "proj__velocity":
		return FlightVelocity, true
	case "proj__prevVelocity":
		return FlightPrevVelocity, true
	case "proj__omega":
		return FlightOmega, true
	case "proj__prevOmega":
		return FlightPrevOmega, true
	default:
		return 0, false
	}
}

// SetPoint3 records one field only when the caller has already decoded an
// ecs2.Point3 and all three values are finite. It returns false for unknown
// fields and invalid values, leaving any earlier evidence untouched.
func (e *FlightEvidence) SetPoint3(field FlightField, value ecsp.Point3) bool {
	if e == nil || !finitePoint3(value) {
		return false
	}
	evidence := Point3Evidence{Present: true, X: value.X, Y: value.Y, Z: value.Z}
	switch field {
	case FlightVelocity:
		e.Velocity = evidence
	case FlightPrevVelocity:
		e.PrevVelocity = evidence
	case FlightOmega:
		e.Omega = evidence
	case FlightPrevOmega:
		e.PrevOmega = evidence
	default:
		return false
	}
	return true
}

func (e *FlightEvidence) Point3(field FlightField) (Point3Evidence, bool) {
	if e == nil {
		return Point3Evidence{}, false
	}
	var value Point3Evidence
	switch field {
	case FlightVelocity:
		value = e.Velocity
	case FlightPrevVelocity:
		value = e.PrevVelocity
	case FlightOmega:
		value = e.Omega
	case FlightPrevOmega:
		value = e.PrevOmega
	default:
		return Point3Evidence{}, false
	}
	return value, value.Present
}

func finitePoint3(value ecsp.Point3) bool {
	return finiteFloat32(value.X) && finiteFloat32(value.Y) && finiteFloat32(value.Z)
}

func finiteFloat32(value float32) bool {
	return !math.IsNaN(float64(value)) && !math.IsInf(float64(value), 0)
}
