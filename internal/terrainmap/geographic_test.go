package terrainmap

import (
	"crypto/sha256"
	"encoding/hex"
	"math"
	"strings"
	"testing"
)

const validGeographicEntry = `{"enabled":true,"reason":"fixture","confidence":"high","method":"affine","local_bounds":[-100,-200,100,200],"geo_bounds":[10,40,20,60],"uncertainty_km":2.5,"source":"fixture/source"}`

func geographicFixture(entry string) []byte {
	return []byte(`{"schema":1,"source":{"repository":"fixture/repository","commit":"0123456789abcdef0123456789abcdef01234567"},"maps":{"berlin":` + entry + `}}`)
}

func TestGeographicLocationAffineCornersCenterAndExtrapolation(t *testing.T) {
	references, commit, err := loadGeographicReferences(geographicFixture(validGeographicEntry))
	if err != nil {
		t.Fatalf("load fixture: %v", err)
	}
	if commit != "0123456789abcdef0123456789abcdef01234567" {
		t.Fatalf("source commit = %q", commit)
	}
	profile := references["berlin"]
	if !profile.Enabled || profile.Confidence != "high" || profile.Method != "affine" ||
		profile.Source != "fixture/source" || profile.UncertaintyKM != 2.5 {
		t.Fatalf("profile = %+v", profile)
	}

	tests := []struct {
		name     string
		x, z     float64
		lat, lon float64
	}{
		{name: "south west", x: -100, z: -200, lat: 40, lon: 10},
		{name: "north east", x: 100, z: 200, lat: 60, lon: 20},
		{name: "center", x: 0, z: 0, lat: 50, lon: 15},
		{name: "outside extrapolation", x: 200, z: -400, lat: 30, lon: 25},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			lat, lon := profile.Location(test.x, test.z)
			if lat != test.lat || lon != test.lon {
				t.Fatalf("Location(%v, %v) = (%v, %v), want (%v, %v)", test.x, test.z, lat, lon, test.lat, test.lon)
			}
		})
	}
}

func TestGeographicDisabledEntryMayOmitBoundsAndStillExists(t *testing.T) {
	for _, confidence := range []string{"low", "unresolved", "unknown"} {
		t.Run(confidence, func(t *testing.T) {
			entry := `{"enabled":false,"reason":"no reliable footprint","confidence":"` + confidence + `","method":"unknown","source":""}`
			references, _, err := loadGeographicReferences(geographicFixture(entry))
			if err != nil {
				t.Fatalf("load disabled fixture: %v", err)
			}
			profile, ok := geographicReferenceFrom(references, "WarThunder.Geo1.BERLIN")
			if !ok {
				t.Fatal("disabled catalog entry must still exist")
			}
			if profile.Enabled {
				t.Fatal("disabled profile was enabled")
			}
			if profile.LocalBounds != ([4]float64{}) || profile.GeoBounds != ([4]float64{}) {
				t.Fatalf("disabled profile invented bounds: local=%v geo=%v", profile.LocalBounds, profile.GeoBounds)
			}
			if profile.Reason != "no reliable footprint" || profile.Confidence != confidence || profile.Method != "unknown" {
				t.Fatalf("disabled profile = %+v", profile)
			}
		})
	}
}

func TestGeographicConfigRejectsBadEntries(t *testing.T) {
	tests := []struct {
		name  string
		entry string
	}{
		{
			name:  "missing enabled",
			entry: `{"reason":"fixture","confidence":"high","method":"affine","local_bounds":[0,0,1,1],"geo_bounds":[0,0,1,1],"source":"fixture"}`,
		},
		{
			name:  "unknown entry field",
			entry: `{"enabled":true,"reason":"fixture","confidence":"high","method":"affine","local_bounds":[0,0,1,1],"geo_bounds":[0,0,1,1],"source":"fixture","extra":1}`,
		},
		{
			name:  "enabled low confidence",
			entry: `{"enabled":true,"reason":"fixture","confidence":"low","method":"affine","local_bounds":[0,0,1,1],"geo_bounds":[0,0,1,1],"source":"fixture"}`,
		},
		{
			name:  "enabled unresolved confidence",
			entry: `{"enabled":true,"reason":"fixture","confidence":"unresolved","method":"affine","local_bounds":[0,0,1,1],"geo_bounds":[0,0,1,1],"source":"fixture"}`,
		},
		{
			name:  "enabled without source",
			entry: `{"enabled":true,"reason":"fixture","confidence":"high","method":"affine","local_bounds":[0,0,1,1],"geo_bounds":[0,0,1,1],"source":""}`,
		},
		{
			name:  "enabled without bounds",
			entry: `{"enabled":true,"reason":"fixture","confidence":"medium","method":"affine","source":"fixture"}`,
		},
		{
			name:  "only one bounds pair",
			entry: `{"enabled":false,"reason":"fixture","confidence":"low","method":"none","local_bounds":[0,0,1,1],"source":""}`,
		},
		{
			name:  "short bounds array",
			entry: `{"enabled":true,"reason":"fixture","confidence":"high","method":"affine","local_bounds":[0,0,1],"geo_bounds":[0,0,1,1],"source":"fixture"}`,
		},
		{
			name:  "long bounds array",
			entry: `{"enabled":true,"reason":"fixture","confidence":"high","method":"affine","local_bounds":[0,0,1,1],"geo_bounds":[0,0,1,1,2],"source":"fixture"}`,
		},
		{
			name:  "reversed local bounds",
			entry: `{"enabled":true,"reason":"fixture","confidence":"high","method":"affine","local_bounds":[1,0,0,1],"geo_bounds":[0,0,1,1],"source":"fixture"}`,
		},
		{
			name:  "reversed geographic bounds",
			entry: `{"enabled":true,"reason":"fixture","confidence":"high","method":"affine","local_bounds":[0,0,1,1],"geo_bounds":[0,1,1,0],"source":"fixture"}`,
		},
		{
			name:  "latitude out of range",
			entry: `{"enabled":true,"reason":"fixture","confidence":"high","method":"affine","local_bounds":[0,0,1,1],"geo_bounds":[0,95,1,100],"source":"fixture"}`,
		},
		{
			name:  "longitude out of range",
			entry: `{"enabled":true,"reason":"fixture","confidence":"high","method":"affine","local_bounds":[0,0,1,1],"geo_bounds":[-180,0,181,1],"source":"fixture"}`,
		},
		{
			name:  "non-finite local bounds",
			entry: `{"enabled":true,"reason":"fixture","confidence":"high","method":"affine","local_bounds":[0,0,1e400,1],"geo_bounds":[0,0,1,1],"source":"fixture"}`,
		},
		{
			name:  "non-finite uncertainty",
			entry: `{"enabled":true,"reason":"fixture","confidence":"high","method":"affine","local_bounds":[0,0,1,1],"geo_bounds":[0,0,1,1],"uncertainty_km":1e400,"source":"fixture"}`,
		},
		{
			name:  "negative uncertainty",
			entry: `{"enabled":true,"reason":"fixture","confidence":"high","method":"affine","local_bounds":[0,0,1,1],"geo_bounds":[0,0,1,1],"uncertainty_km":-1,"source":"fixture"}`,
		},
		{
			name:  "invalid confidence",
			entry: `{"enabled":false,"reason":"fixture","confidence":"certain","method":"none","source":""}`,
		},
		{
			name:  "empty method",
			entry: `{"enabled":false,"reason":"fixture","confidence":"low","method":"","source":""}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, _, err := loadGeographicReferences(geographicFixture(test.entry)); err == nil {
				t.Fatal("invalid entry was accepted")
			}
		})
	}
}

func TestGeographicConfigRejectsMalformedDocuments(t *testing.T) {
	documents := []struct {
		name string
		data string
	}{
		{name: "invalid JSON", data: `{`},
		{
			name: "wrong schema",
			data: `{"schema":2,"source":{"repository":"fixture","commit":"fixture"},"maps":{}}`,
		},
		{name: "missing maps", data: `{"schema":1,"source":{"repository":"fixture","commit":"fixture"}}`},
		{name: "null maps", data: `{"schema":1,"source":{"repository":"fixture","commit":"fixture"},"maps":null}`},
		{name: "missing source", data: `{"schema":1,"source":{},"maps":{}}`},
		{name: "short source commit", data: `{"schema":1,"source":{"repository":"fixture","commit":"0123456789abcdef"},"maps":{}}`},
		{name: "non-hex source commit", data: `{"schema":1,"source":{"repository":"fixture","commit":"0123456789abcdef0123456789abcdef0123456z"},"maps":{}}`},
		{name: "unknown top-level field", data: `{"schema":1,"source":{"repository":"fixture","commit":"fixture"},"maps":{},"extra":true}`},
		{name: "trailing value", data: `{"schema":1,"source":{"repository":"fixture","commit":"fixture"},"maps":{}} {}`},
		{
			name: "duplicate key",
			data: `{"schema":1,"source":{"repository":"fixture","commit":"fixture"},"maps":{"berlin":` + validGeographicEntry + `,"berlin":` + validGeographicEntry + `}}`,
		},
		{
			name: "unknown map ID",
			data: `{"schema":1,"source":{"repository":"fixture","commit":"fixture"},"maps":{"not_a_map":` + validGeographicEntry + `}}`,
		},
	}

	for _, test := range documents {
		t.Run(test.name, func(t *testing.T) {
			if _, _, err := loadGeographicReferences([]byte(test.data)); err == nil {
				t.Fatal("invalid document was accepted")
			}
		})
	}
}

func TestGeographicBoundsValidationRejectsNonFiniteValues(t *testing.T) {
	local := [4]float64{0, math.NaN(), 1, 1}
	geo := [4]float64{0, 0, 1, 1}
	if err := validateGeographicBounds("fixture", local, geo); err == nil || !strings.Contains(err.Error(), "not finite") {
		t.Fatalf("NaN bounds error = %v", err)
	}
	local = [4]float64{0, 0, 1, 1}
	geo = [4]float64{0, 0, math.Inf(1), 1}
	if err := validateGeographicBounds("fixture", local, geo); err == nil || !strings.Contains(err.Error(), "not finite") {
		t.Fatalf("infinite bounds error = %v", err)
	}
}

func TestGeographicConfigMetadataAndPrefix(t *testing.T) {
	sum := sha256.Sum256(geographicReferencesJSON)
	if got, want := GeographicConfigSHA256(), hex.EncodeToString(sum[:]); got != want {
		t.Fatalf("config SHA-256 = %q, want %q", got, want)
	}
	_, commit, err := loadGeographicReferences(geographicReferencesJSON)
	if err != nil {
		t.Fatalf("load embedded configuration: %v", err)
	}
	if got := GeographicSourceCommit(); got != commit {
		t.Fatalf("source commit = %q, want %q", got, commit)
	}
	if got := FormatGeographicMapID("berlin"); got != "WarThunder.Geo1.berlin" {
		t.Fatalf("FormatGeographicMapID = %q", got)
	}
	if got, err := NormalizeMapID("WarThunder.Geo1.BERLIN"); err != nil || got != "berlin" {
		t.Fatalf("NormalizeMapID Geo1 = %q, %v", got, err)
	}
	if _, ok := GeographicReference("WarThunder.Geo1.not_a_map"); ok {
		t.Fatal("unknown map reported as present")
	}
}
