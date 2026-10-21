package terrainmap

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	geographicConfigSchema = 1
	geographicMapIDPrefix  = "WarThunder.Geo1."
)

// GeographicProfile is one map's affine placement between local terrain
// coordinates and geographic coordinates. Enabled is false when the catalog
// deliberately has no trustworthy geographic placement for the map.
type GeographicProfile struct {
	Enabled       bool
	Reason        string
	Confidence    string
	Method        string
	Source        string
	LocalBounds   [4]float64
	GeoBounds     [4]float64
	UncertaintyKM float64
}

type geographicConfigSource struct {
	Repository string `json:"repository"`
	Commit     string `json:"commit"`
}

type geographicConfigEntry struct {
	Enabled       *bool             `json:"enabled"`
	Reason        string            `json:"reason"`
	Confidence    string            `json:"confidence"`
	Method        string            `json:"method"`
	LocalBounds   *geographicBounds `json:"local_bounds"`
	GeoBounds     *geographicBounds `json:"geo_bounds"`
	UncertaintyKM *float64          `json:"uncertainty_km"`
	Source        string            `json:"source"`
}

type geographicConfigDocument struct {
	Schema int                              `json:"schema"`
	Source geographicConfigSource           `json:"source"`
	Maps   map[string]geographicConfigEntry `json:"maps"`
}

// geographicBounds has its own decoder because encoding/json otherwise
// truncates an array shorter or longer than four elements.
type geographicBounds [4]float64

func (bounds *geographicBounds) UnmarshalJSON(data []byte) error {
	var values []float64
	if err := json.Unmarshal(data, &values); err != nil {
		return err
	}
	if len(values) != len(bounds) {
		return fmt.Errorf("must contain exactly 4 numbers, got %d", len(values))
	}
	copy(bounds[:], values)
	return nil
}

//go:embed geographic_references.json
var geographicReferencesJSON []byte

var (
	geographicReferences        map[string]GeographicProfile
	geographicSourceCommitValue string
	geographicConfigSHA256Value = geographicConfigSHA256(geographicReferencesJSON)
)

func init() {
	references, commit, err := loadGeographicReferences(geographicReferencesJSON)
	if err != nil {
		panic(fmt.Sprintf("terrainmap: invalid embedded geographic_references.json: %v", err))
	}
	geographicReferences = references
	geographicSourceCommitValue = commit
}

func geographicConfigSHA256(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// GeographicConfigSHA256 returns the SHA-256 of the embedded configuration's
// original bytes. Consumers can use it to verify that they refer to the same
// geographic data revision as this binary.
func GeographicConfigSHA256() string {
	return geographicConfigSHA256Value
}

// GeographicSourceCommit returns the source commit recorded at the top level
// of the embedded geographic configuration.
func GeographicSourceCommit() string {
	return geographicSourceCommitValue
}

// FormatGeographicMapID returns the canonical MapId for a bare terrain id.
func FormatGeographicMapID(id string) string {
	return geographicMapIDPrefix + id
}

// GeographicReference returns the geographic profile for id. The second
// result reports catalog existence, including entries explicitly disabled by
// the configuration.
func GeographicReference(id string) (GeographicProfile, bool) {
	return geographicReferenceFrom(geographicReferences, id)
}

func geographicReferenceFrom(references map[string]GeographicProfile, id string) (GeographicProfile, bool) {
	canonical, err := NormalizeMapID(id)
	if err != nil {
		return GeographicProfile{}, false
	}
	profile, ok := references[canonical]
	return profile, ok
}

// Location maps a local terrain X/Z coordinate through the profile's affine
// bounding-box transform. Points outside the footprint are extrapolated; no
// component is clamped. Callers decide whether a disabled profile is usable.
func (profile GeographicProfile) Location(x, z float64) (latitude, longitude float64) {
	u := (x - profile.LocalBounds[0]) / (profile.LocalBounds[2] - profile.LocalBounds[0])
	v := (z - profile.LocalBounds[1]) / (profile.LocalBounds[3] - profile.LocalBounds[1])
	longitude = profile.GeoBounds[0] + u*(profile.GeoBounds[2]-profile.GeoBounds[0])
	latitude = profile.GeoBounds[1] + v*(profile.GeoBounds[3]-profile.GeoBounds[1])
	return latitude, longitude
}

func loadGeographicReferences(data []byte) (map[string]GeographicProfile, string, error) {
	if !utf8.Valid(data) {
		return nil, "", fmt.Errorf("configuration is not valid UTF-8")
	}
	if err := validateGeographicJSON(data); err != nil {
		return nil, "", err
	}

	var document geographicConfigDocument
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&document); err != nil {
		return nil, "", fmt.Errorf("decode configuration: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return nil, "", fmt.Errorf("configuration has a second JSON value")
		}
		return nil, "", fmt.Errorf("decode trailing configuration: %w", err)
	}

	if document.Schema != geographicConfigSchema {
		return nil, "", fmt.Errorf("schema = %d, want %d", document.Schema, geographicConfigSchema)
	}
	if err := validateGeographicConfigText("source.repository", document.Source.Repository, 512); err != nil {
		return nil, "", err
	}
	if err := validateGeographicConfigText("source.commit", document.Source.Commit, 40); err != nil {
		return nil, "", err
	}
	if commitBytes, err := hex.DecodeString(document.Source.Commit); err != nil || len(commitBytes) != 20 {
		return nil, "", fmt.Errorf("source.commit must be a complete 40-character hexadecimal SHA")
	}
	if document.Maps == nil {
		return nil, "", fmt.Errorf("maps is missing")
	}

	ids := make([]string, 0, len(document.Maps))
	for id := range document.Maps {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	references := make(map[string]GeographicProfile, len(document.Maps))
	for _, id := range ids {
		if !isCatalogID(id) {
			return nil, "", fmt.Errorf("maps contains non-canonical catalog id %q", id)
		}
		profile, err := validateGeographicConfigEntry(id, document.Maps[id])
		if err != nil {
			return nil, "", err
		}
		references[id] = profile
	}
	return references, document.Source.Commit, nil
}

func validateGeographicConfigEntry(id string, entry geographicConfigEntry) (GeographicProfile, error) {
	label := "maps[" + id + "]"
	if entry.Enabled == nil {
		return GeographicProfile{}, fmt.Errorf("%s.enabled is missing", label)
	}
	for name, value := range map[string]string{
		"confidence": entry.Confidence,
		"method":     entry.Method,
	} {
		if err := validateGeographicConfigText(label+"."+name, value, 1024); err != nil {
			return GeographicProfile{}, err
		}
	}
	if entry.Reason != "" {
		if err := validateGeographicConfigText(label+".reason", entry.Reason, 1024); err != nil {
			return GeographicProfile{}, err
		}
	}
	if entry.Source != "" {
		if err := validateGeographicConfigText(label+".source", entry.Source, 2048); err != nil {
			return GeographicProfile{}, err
		}
	}
	switch entry.Confidence {
	case "high", "medium", "low", "unresolved", "unknown":
	default:
		return GeographicProfile{}, fmt.Errorf("%s.confidence %q is invalid", label, entry.Confidence)
	}

	enabled := *entry.Enabled
	if enabled {
		if entry.Confidence != "high" && entry.Confidence != "medium" {
			return GeographicProfile{}, fmt.Errorf("%s is enabled at %s confidence; enabled maps require high or medium confidence", label, entry.Confidence)
		}
		if entry.Source == "" {
			return GeographicProfile{}, fmt.Errorf("%s is enabled without a source", label)
		}
	}
	if (entry.LocalBounds == nil) != (entry.GeoBounds == nil) {
		return GeographicProfile{}, fmt.Errorf("%s must provide both local_bounds and geo_bounds or neither", label)
	}
	if enabled && entry.LocalBounds == nil {
		return GeographicProfile{}, fmt.Errorf("%s is enabled without bounds", label)
	}

	profile := GeographicProfile{
		Enabled:    enabled,
		Reason:     entry.Reason,
		Confidence: entry.Confidence,
		Method:     entry.Method,
		Source:     entry.Source,
	}
	if entry.LocalBounds != nil {
		profile.LocalBounds = [4]float64(*entry.LocalBounds)
		profile.GeoBounds = [4]float64(*entry.GeoBounds)
		if err := validateGeographicBounds(label, profile.LocalBounds, profile.GeoBounds); err != nil {
			return GeographicProfile{}, err
		}
	}
	if entry.UncertaintyKM != nil {
		value := *entry.UncertaintyKM
		if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
			return GeographicProfile{}, fmt.Errorf("%s.uncertainty_km must be finite and non-negative", label)
		}
		profile.UncertaintyKM = value
	}
	return profile, nil
}

func validateGeographicBounds(label string, local, geo [4]float64) error {
	for i, value := range local {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return fmt.Errorf("%s.local_bounds[%d] is not finite", label, i)
		}
	}
	for i, value := range geo {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return fmt.Errorf("%s.geo_bounds[%d] is not finite", label, i)
		}
	}
	if local[0] >= local[2] || local[1] >= local[3] {
		return fmt.Errorf("%s.local_bounds must satisfy minX < maxX and minZ < maxZ", label)
	}
	if geo[0] >= geo[2] || geo[1] >= geo[3] {
		return fmt.Errorf("%s.geo_bounds must satisfy minLon < maxLon and minLat < maxLat", label)
	}
	if geo[1] < -90 || geo[1] > 90 || geo[3] < -90 || geo[3] > 90 {
		return fmt.Errorf("%s.geo_bounds latitude must be within [-90, 90]", label)
	}
	if geo[0] < -180 || geo[0] > 180 || geo[2] < -180 || geo[2] > 180 {
		return fmt.Errorf("%s.geo_bounds longitude must be within [-180, 180]", label)
	}
	return nil
}

func validateGeographicConfigText(label, value string, maxBytes int) error {
	if value == "" {
		return fmt.Errorf("%s is empty", label)
	}
	if len(value) > maxBytes {
		return fmt.Errorf("%s exceeds %d bytes", label, maxBytes)
	}
	if !utf8.ValidString(value) {
		return fmt.Errorf("%s is not valid UTF-8", label)
	}
	if strings.TrimSpace(value) != value {
		return fmt.Errorf("%s has leading or trailing whitespace", label)
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return fmt.Errorf("%s contains a control character", label)
		}
	}
	return nil
}

func isCatalogID(id string) bool {
	for _, knownID := range knownIDs {
		if id == knownID {
			return true
		}
	}
	return false
}

// validateGeographicJSON rejects duplicate object keys and extra top-level
// values before the typed decoder applies DisallowUnknownFields.
func validateGeographicJSON(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := scanGeographicJSONValue(decoder); err != nil {
		return fmt.Errorf("invalid configuration JSON: %w", err)
	}
	if _, err := decoder.Token(); err != io.EOF {
		if err == nil {
			return fmt.Errorf("configuration has a second JSON value")
		}
		return fmt.Errorf("invalid trailing configuration JSON: %w", err)
	}
	return nil
}

func scanGeographicJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		seen := map[string]struct{}{}
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return fmt.Errorf("object key is not a string")
			}
			if _, exists := seen[key]; exists {
				return fmt.Errorf("duplicate object key %q", key)
			}
			seen[key] = struct{}{}
			if err := scanGeographicJSONValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil {
			return err
		}
		if closing != json.Delim('}') {
			return fmt.Errorf("object is not closed")
		}
	case '[':
		for decoder.More() {
			if err := scanGeographicJSONValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil {
			return err
		}
		if closing != json.Delim(']') {
			return fmt.Errorf("array is not closed")
		}
	default:
		return fmt.Errorf("unexpected delimiter %q", delimiter)
	}
	return nil
}
