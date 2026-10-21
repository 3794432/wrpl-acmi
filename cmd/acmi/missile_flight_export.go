package main

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
)

// Preserve native horizontal coordinates for Tacview's trajectory calculations.
// Unknown body attitude is omitted instead of being asserted to be zero.
func acmiMissilePositionValue(s acmiSample) string {
	lat, lon := terrainPosition(s.x, s.z)
	return fmt.Sprintf("%.7f|%.7f|%.2f|%.3f|%.3f", lon, lat, *alt0+s.y, s.x, s.z)
}

var missileFlightFields = []string{
	"VelocityX", "VelocityY", "VelocityZ", "Speed", "GroundSpeed", "ClimbRate",
	"Course", "FlightPath", "AccelerationX", "AccelerationY", "AccelerationZ",
	"Acceleration", "KinematicG", "NormalAccelerationG", "TurnRate",
}

func missileFlightValues(f missileFlightSample) []string {
	values := make([]string, len(missileFlightFields))
	put := func(i int, value float64) { values[i] = strconv.FormatFloat(value, 'f', 6, 64) }
	if f.Valid {
		for i, value := range f.Velocity {
			put(i, value)
		}
		put(3, f.Speed)
		put(4, f.GroundSpeed)
		put(5, f.ClimbRate)
		if f.HasDirection {
			put(6, f.Course)
		}
		if f.HasFlightPath {
			put(7, f.FlightPath)
		}
		if f.HasAcceleration {
			for i, value := range f.Acceleration {
				put(8+i, value)
			}
			put(11, f.AccelMagnitude)
			put(12, f.AccelG)
			put(13, f.NormalG)
		}
		if f.HasTurnRate {
			put(14, f.TurnRate)
		}
	}
	return values
}

func missileFlightProperties(f missileFlightSample) []string {
	return missileFlightPropertiesWithINS(f, acmiSample{})
}

func missileFlightPropertiesWithINS(f missileFlightSample, s acmiSample) []string {
	valid := "0"
	if f.Valid {
		valid = "1"
	}
	props := []string{"WT_FlightSource=PositionDifferenceLocalFit", "WT_FlightValid=" + valid}
	for i, value := range missileFlightValues(f) {
		// Empty assignments clear persistent values at gaps and missing estimates.
		props = append(props, "WT_"+missileFlightFields[i]+"="+value)
	}
	ins := missileINSReadingForSample(s)
	props = append(props,
		"WT_SeekerTimer="+ins.seekerTimerValue(),
		"WT_InertialTime="+ins.inertialTimeValue(),
		"WT_InertialTimeValid="+ins.validValue(),
		"WT_InertialTimeSource=SeekerTimer48HzInferred",
	)
	label := "INS -- s"
	if ins.valid {
		label = fmt.Sprintf("INS %.2f s", ins.seconds)
	}
	return append(props, "Label="+escapeACMIValue(label))
}

func writeMissileFlightLog(path string, tracks []*track) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := csv.NewWriter(f)
	header := []string{"missile_source_id", "time_ms", "x_m", "y_m", "z_m", "source", "valid"}
	header = append(header, missileFlightFields...)
	header = append(header, "recorded_sample_json", "creation_evidence_json")
	header = append(header, "WT_SeekerTimer", "WT_InertialTime", "WT_InertialTimeValid")
	guidanceColumns := tracksHaveMissileGuidance(tracks)
	if guidanceColumns {
		header = append(header, "guidance_type", "guidance_source", "guidance_confidence", "guidance_candidates")
	}
	if err := w.Write(header); err != nil {
		return err
	}
	for _, tr := range tracks {
		if tr == nil || !tr.key.missile {
			continue
		}
		flight := enrichRecordedMissileFlight(tr.samples)
		for i, s := range tr.samples {
			ins := missileINSReadingForSample(s)
			row := []string{strconv.FormatUint(tr.key.id, 10), strconv.FormatUint(uint64(s.t), 10),
				strconv.FormatFloat(s.x, 'g', -1, 64), strconv.FormatFloat(s.y, 'g', -1, 64),
				strconv.FormatFloat(s.z, 'g', -1, 64), "PositionDifferenceLocalFit", strconv.FormatBool(flight[i].Valid)}
			row = append(row, missileFlightValues(flight[i])...)
			raw := ""
			if s.projectile != nil {
				encoded, err := json.Marshal(s.projectile)
				if err != nil {
					return fmt.Errorf("encoding missile %d at %d: %w", tr.key.id, s.t, err)
				}
				raw = string(encoded)
			}
			creation := ""
			if i == 0 && tr.projectileSource != nil {
				encoded, err := json.Marshal(tr.projectileSource)
				if err != nil {
					return err
				}
				creation = string(encoded)
			}
			row = append(row, raw, creation)
			row = append(row, ins.seekerTimerValue(), ins.inertialTimeValue(), ins.validValue())
			if guidanceColumns {
				row = append(row, missileGuidanceCSVValues(tr)...)
			}
			if err := w.Write(row); err != nil {
				return err
			}
		}
	}
	w.Flush()
	return w.Error()
}
