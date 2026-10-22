package terrainbundle

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

type bundleInput struct {
	diskPath    string
	archiveName string
	size        int64
	sha256      string
}

type mapOutputMetadata struct {
	Width      int64  `json:"width"`
	Height     int64  `json:"height"`
	DataBytes  int64  `json:"data_bytes"`
	DataSHA256 string `json:"data_sha256"`
}

type mapMetadata struct {
	ID     string            `json:"id"`
	Status string            `json:"status"`
	Output mapOutputMetadata `json:"output"`
}

func discoverInputs(sourceRoot string) ([]bundleInput, error) {
	rootEntries, err := requiredDirectoryEntries(sourceRoot, []string{"AtlasReferences.json", "GeoReferences.json", "maps"})
	if err != nil {
		return nil, err
	}
	for _, name := range []string{"AtlasReferences.json", "GeoReferences.json"} {
		info := rootEntries[name]
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return nil, fmt.Errorf("%s must be a regular file and not a symlink", name)
		}
	}
	if mapsInfo := rootEntries["maps"]; mapsInfo.Mode()&os.ModeSymlink != 0 || !mapsInfo.IsDir() {
		return nil, errors.New("maps must be a real directory")
	}

	atlasPath := filepath.Join(sourceRoot, "AtlasReferences.json")
	ids, atlasDigest, err := loadAtlasReferences(atlasPath)
	if err != nil {
		return nil, err
	}
	geoPath := filepath.Join(sourceRoot, "GeoReferences.json")
	geoDigest, err := validateGeoReferences(geoPath)
	if err != nil {
		return nil, err
	}
	mapDir := filepath.Join(sourceRoot, "maps")
	if err := validateMapsDirectory(mapDir, ids); err != nil {
		return nil, err
	}

	inputs := []bundleInput{
		{diskPath: atlasPath, archiveName: "AtlasReferences.json", sha256: fmt.Sprintf("%x", atlasDigest)},
		{diskPath: geoPath, archiveName: "GeoReferences.json", sha256: fmt.Sprintf("%x", geoDigest)},
	}
	for _, id := range ids {
		folder := filepath.Join(mapDir, id)
		info, err := os.Lstat(folder)
		if err != nil {
			return nil, fmt.Errorf("maps/%s: %w", id, err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return nil, fmt.Errorf("maps/%s must be a real directory", id)
		}
		fileNames := []string{"metadata.json", id + ".data", "CustomHeightmapList.xml"}
		entries, err := requiredDirectoryEntries(folder, fileNames)
		if err != nil {
			return nil, err
		}
		for _, name := range fileNames {
			if entries[name].Mode()&os.ModeSymlink != 0 || !entries[name].Mode().IsRegular() {
				return nil, fmt.Errorf("maps/%s/%s must be a regular file and not a symlink", id, name)
			}
		}

		metadataPath := filepath.Join(folder, "metadata.json")
		metadataData, err := readRegularLimited(metadataPath, maxMetadataBytes)
		if err != nil {
			return nil, fmt.Errorf("maps/%s/metadata.json: %w", id, err)
		}
		metadata, err := decodeMapMetadata(metadataData)
		if err != nil {
			return nil, fmt.Errorf("maps/%s/metadata.json: %w", id, err)
		}
		if metadata.ID != id {
			return nil, fmt.Errorf("maps/%s/metadata.json: id is %q", id, metadata.ID)
		}
		if metadata.Status != "ok" {
			return nil, fmt.Errorf("maps/%s/metadata.json: status is %q, want ok", id, metadata.Status)
		}
		if metadata.Output.Width < 2 || metadata.Output.Height < 2 ||
			metadata.Output.Width > math.MaxInt64/metadata.Output.Height ||
			metadata.Output.Width*metadata.Output.Height > math.MaxInt64/2 {
			return nil, fmt.Errorf("maps/%s/metadata.json: dimensions are invalid", id)
		}
		expectedBytes := metadata.Output.Width * metadata.Output.Height * 2
		if metadata.Output.DataBytes != expectedBytes {
			return nil, fmt.Errorf("maps/%s/metadata.json: data_bytes is %d, want %d", id, metadata.Output.DataBytes, expectedBytes)
		}
		if err := validateSHA256Text(metadata.Output.DataSHA256); err != nil {
			return nil, fmt.Errorf("maps/%s/metadata.json: data_sha256: %w", id, err)
		}
		dataPath := filepath.Join(folder, id+".data")
		xmlPath := filepath.Join(folder, "CustomHeightmapList.xml")
		dataInfo, err := os.Lstat(dataPath)
		if err != nil {
			return nil, fmt.Errorf("maps/%s/%s.data: %w", id, id, err)
		}
		if dataInfo.Mode()&os.ModeSymlink != 0 || !dataInfo.Mode().IsRegular() {
			return nil, fmt.Errorf("maps/%s/%s.data must be a regular file and not a symlink", id, id)
		}
		if dataInfo.Size() != expectedBytes {
			return nil, fmt.Errorf("maps/%s/%s.data is %d bytes, metadata expects %d", id, id, dataInfo.Size(), expectedBytes)
		}

		inputs = append(inputs,
			bundleInput{diskPath: metadataPath, archiveName: fmt.Sprintf("maps/%s/metadata.json", id), sha256: fmt.Sprintf("%x", sha256.Sum256(metadataData))},
			bundleInput{diskPath: dataPath, archiveName: fmt.Sprintf("maps/%s/%s.data", id, id), sha256: metadata.Output.DataSHA256},
			bundleInput{diskPath: xmlPath, archiveName: fmt.Sprintf("maps/%s/CustomHeightmapList.xml", id)},
		)
	}

	if len(inputs) > maxEntries {
		return nil, fmt.Errorf("source contains %d payload entries; maximum is %d", len(inputs), maxEntries)
	}
	var total int64
	for index := range inputs {
		info, err := inspectSingleRegularFile(inputs[index].diskPath)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", inputs[index].archiveName, err)
		}
		if info.Size() > maxFileBytes {
			return nil, fmt.Errorf("%s is %d bytes; maximum is %d", inputs[index].archiveName, info.Size(), maxFileBytes)
		}
		if total > maxTotalBytes-info.Size() {
			return nil, fmt.Errorf("source exceeds %d total bytes", maxTotalBytes)
		}
		if inputs[index].sha256 == "" {
			digest, err := hashRegularFile(inputs[index].diskPath, info.Size())
			if err != nil {
				return nil, fmt.Errorf("%s: %w", inputs[index].archiveName, err)
			}
			inputs[index].sha256 = fmt.Sprintf("%x", digest)
		}
		total += info.Size()
		inputs[index].size = info.Size()
	}
	return inputs, nil
}

func loadAtlasReferences(name string) ([]string, [32]byte, error) {
	var fileDigest [32]byte
	data, err := readRegularLimited(name, maxReferenceBytes)
	if err != nil {
		return nil, fileDigest, fmt.Errorf("AtlasReferences.json: %w", err)
	}
	fileDigest = sha256.Sum256(data)
	if err := validateStrictJSON(data); err != nil {
		return nil, fileDigest, fmt.Errorf("AtlasReferences.json: %w", err)
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal(data, &document); err != nil {
		return nil, fileDigest, fmt.Errorf("AtlasReferences.json: %w", err)
	}
	var references map[string]json.RawMessage
	if rawMaps, wrapped := document["maps"]; wrapped {
		if err := json.Unmarshal(rawMaps, &references); err != nil || references == nil {
			return nil, fileDigest, errors.New("AtlasReferences.json: maps must be an object")
		}
	} else {
		references = document
	}
	if len(references) == 0 || len(references) > maxMapCount {
		return nil, fileDigest, fmt.Errorf("AtlasReferences.json has %d maps; want between 1 and %d", len(references), maxMapCount)
	}
	ids := make([]string, 0, len(references))
	for id, raw := range references {
		if err := validateMapID(id); err != nil {
			return nil, fileDigest, fmt.Errorf("AtlasReferences.json: %w", err)
		}
		var coordinates struct {
			Latitude  *float64 `json:"latitude"`
			Longitude *float64 `json:"longitude"`
		}
		if err := json.Unmarshal(raw, &coordinates); err != nil {
			return nil, fileDigest, fmt.Errorf("AtlasReferences.json[%s]: %w", id, err)
		}
		if coordinates.Latitude == nil || coordinates.Longitude == nil {
			return nil, fileDigest, fmt.Errorf("AtlasReferences.json[%s]: latitude and longitude are required", id)
		}
		if math.IsNaN(*coordinates.Latitude) || math.IsInf(*coordinates.Latitude, 0) || *coordinates.Latitude < -90 || *coordinates.Latitude > 90 {
			return nil, fileDigest, fmt.Errorf("AtlasReferences.json[%s]: latitude is out of range", id)
		}
		if math.IsNaN(*coordinates.Longitude) || math.IsInf(*coordinates.Longitude, 0) || *coordinates.Longitude < -180 || *coordinates.Longitude > 180 {
			return nil, fileDigest, fmt.Errorf("AtlasReferences.json[%s]: longitude is out of range", id)
		}
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids, fileDigest, nil
}

func validateGeoReferences(name string) ([32]byte, error) {
	var fileDigest [32]byte
	data, err := readRegularLimited(name, maxReferenceBytes)
	if err != nil {
		return fileDigest, fmt.Errorf("GeoReferences.json: %w", err)
	}
	fileDigest = sha256.Sum256(data)
	if err := validateStrictJSON(data); err != nil {
		return fileDigest, fmt.Errorf("GeoReferences.json: %w", err)
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal(data, &document); err != nil {
		return fileDigest, fmt.Errorf("GeoReferences.json: %w", err)
	}
	rawMaps, ok := document["maps"]
	if !ok {
		return fileDigest, errors.New("GeoReferences.json: maps is missing")
	}
	var maps map[string]json.RawMessage
	if err := json.Unmarshal(rawMaps, &maps); err != nil || maps == nil {
		return fileDigest, errors.New("GeoReferences.json: maps must be an object")
	}
	for id := range maps {
		if err := validateMapID(id); err != nil {
			return fileDigest, fmt.Errorf("GeoReferences.json: %w", err)
		}
	}
	return fileDigest, nil
}

func hashRegularFile(name string, expectedSize int64) ([32]byte, error) {
	var digest [32]byte
	source, err := inspectRegularFile(name)
	if err != nil {
		return digest, err
	}
	defer source.file.Close()
	if source.info.Size() != expectedSize {
		return digest, fmt.Errorf("size changed from %d to %d", expectedSize, source.info.Size())
	}
	h := sha256.New()
	written, err := copyStream(h, source.file)
	if err != nil {
		return digest, err
	}
	if written != expectedSize {
		return digest, fmt.Errorf("read %d bytes, expected %d", written, expectedSize)
	}
	after, err := source.file.Stat()
	if err != nil {
		return digest, err
	}
	if !os.SameFile(source.info, after) || after.Size() != expectedSize {
		return digest, errors.New("source file changed while hashing")
	}
	copy(digest[:], h.Sum(nil))
	return digest, nil
}

func decodeMapMetadata(data []byte) (mapMetadata, error) {
	if err := validateStrictJSON(data); err != nil {
		return mapMetadata{}, err
	}
	var metadata mapMetadata
	if err := json.Unmarshal(data, &metadata); err != nil {
		return mapMetadata{}, err
	}
	return metadata, nil
}

func validateStrictJSON(data []byte) error {
	if !utf8.Valid(data) {
		return errors.New("JSON is not valid UTF-8")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := scanJSONValue(decoder, 0); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		if err == nil {
			return errors.New("JSON has a second value")
		}
		return fmt.Errorf("invalid trailing JSON: %w", err)
	}
	return nil
}

func scanJSONValue(decoder *json.Decoder, depth int) error {
	if depth > 64 {
		return errors.New("JSON nesting exceeds 64 levels")
	}
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, compound := token.(json.Delim)
	if !compound {
		return nil
	}
	switch delimiter {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return errors.New("JSON object key is not a string")
			}
			if _, exists := seen[key]; exists {
				return fmt.Errorf("duplicate JSON key %q", key)
			}
			seen[key] = struct{}{}
			if err := scanJSONValue(decoder, depth+1); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim('}') {
			return errors.New("JSON object is not closed")
		}
	case '[':
		for decoder.More() {
			if err := scanJSONValue(decoder, depth+1); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim(']') {
			return errors.New("JSON array is not closed")
		}
	default:
		return fmt.Errorf("unexpected JSON delimiter %q", delimiter)
	}
	return nil
}

func readRegularLimited(name string, limit int64) ([]byte, error) {
	source, err := inspectRegularFile(name)
	if err != nil {
		return nil, err
	}
	defer source.file.Close()
	if source.info.Size() > limit {
		return nil, fmt.Errorf("file is %d bytes; maximum is %d", source.info.Size(), limit)
	}
	data, err := io.ReadAll(io.LimitReader(source.file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("file exceeds %d bytes", limit)
	}
	return data, nil
}

func inspectSingleRegularFile(name string) (os.FileInfo, error) {
	info, err := os.Lstat(name)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%q must be a regular file and not a symlink", name)
	}
	return info, nil
}

// requiredDirectoryEntries validates the whitelist that Pack consumes while
// allowing unrelated regular files and directories produced by the converter.
// It never descends into or reads those unrelated entries.
func requiredDirectoryEntries(directory string, required []string) (map[string]os.FileInfo, error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, err
	}
	got := make(map[string]os.FileInfo, len(entries))
	for _, entry := range entries {
		info, err := os.Lstat(filepath.Join(directory, entry.Name()))
		if err != nil {
			return nil, err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("%s contains symlink %q", directory, entry.Name())
		}
		if !info.IsDir() && !info.Mode().IsRegular() {
			return nil, fmt.Errorf("%s contains special file %q", directory, entry.Name())
		}
		got[entry.Name()] = info
	}
	for _, name := range required {
		if _, ok := got[name]; !ok {
			return nil, fmt.Errorf("%s is missing %q", directory, name)
		}
	}
	return got, nil
}

func validateMapsDirectory(directory string, ids []string) error {
	for _, id := range ids {
		info, err := os.Lstat(filepath.Join(directory, id))
		if err != nil {
			if os.IsNotExist(err) {
				return fmt.Errorf("%s is missing map directory %q", directory, id)
			}
			return fmt.Errorf("inspect map directory %q: %w", id, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("map directory %q is a symlink", id)
		}
		if !info.IsDir() {
			return fmt.Errorf("map directory %q is not a directory", id)
		}
	}
	return nil
}

func validateMapID(id string) error {
	if len(id) == 0 || len(id) > 128 {
		return fmt.Errorf("map id %q has an invalid length", id)
	}
	if id[0] < 'a' || id[0] > 'z' {
		return fmt.Errorf("map id %q must start with a lowercase ASCII letter", id)
	}
	for _, r := range id {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '_' {
			return fmt.Errorf("map id %q is not a safe lowercase identifier", id)
		}
	}
	return nil
}

func validateSHA256Text(value string) error {
	if len(value) != 64 {
		return errors.New("must contain 64 lowercase hexadecimal characters")
	}
	if _, err := hex.DecodeString(value); err != nil || strings.ToLower(value) != value {
		return errors.New("must contain 64 lowercase hexadecimal characters")
	}
	return nil
}
