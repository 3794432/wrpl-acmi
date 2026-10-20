package main

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/maxsupermanhd/wrpl-inspector/v3/internal/terrainmap"
)

const (
	maxLineBytes = 1 << 20
	dataSource   = "War Thunder replay via wrpl-inspector"
	layoutValue  = "AtlasV1"
)

type scanInfo struct {
	mapIDs    map[string]struct{}
	levels    map[string]struct{}
	invalidID string
	hasSource bool
}

type transformInfo struct {
	transformed int
	globals     int
	inserted    int
}

func scanACMI(r io.Reader) (scanInfo, error) {
	info := scanInfo{mapIDs: map[string]struct{}{}, levels: map[string]struct{}{}}
	br := bufio.NewReaderSize(r, 64*1024)
	headerBytes := 0
	inTimeline := false
	sawType := false
	for {
		raw, err := readLine(br)
		if len(raw) > 0 {
			content := trimEOL(raw)
			clean := strings.TrimSpace(stripBOM(content))
			if !inTimeline && strings.HasPrefix(clean, "#") {
				inTimeline = true
			}
			if !inTimeline {
				headerBytes += len(raw)
				if headerBytes > maxLineBytes {
					return info, fmt.Errorf("ACMI header exceeds %d bytes", maxLineBytes)
				}
				if !sawType && clean != "" {
					if clean != "FileType=text/acmi/tacview" {
						return info, errors.New("not a text .acmi file (expected FileType=text/acmi/tacview)")
					}
					sawType = true
				}
			}
			fields := splitProperties(content)
			if len(fields) > 1 && strings.TrimSpace(stripBOM(fields[0])) == "0" {
				for _, field := range fields[1:] {
					name, value, ok := property(field)
					if !ok {
						continue
					}
					switch name {
					case "MapId":
						if id, err := terrainmap.NormalizeMapID(value); err == nil {
							info.mapIDs[id] = struct{}{}
						} else if info.invalidID == "" {
							info.invalidID = value
						}
					case "SourceLevel", "Level":
						if !inTimeline {
							if id, ok := terrainmap.FromLevel(value); ok {
								info.levels[id] = struct{}{}
							}
						}
					case "DataSource":
						if strings.Contains(value, dataSource) {
							info.hasSource = true
						}
					}
				}
			}
			for _, field := range fields {
				name, value, ok := property(field)
				if ok && name == "T" {
					if err := validateT(value); err != nil {
						return info, err
					}
				}
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return info, err
		}
	}
	if !sawType {
		return info, errors.New("not a text .acmi file")
	}
	if !info.hasSource {
		return info, fmt.Errorf("unsupported source: DataSource must contain %q", dataSource)
	}
	return info, nil
}

func chooseMapID(info scanInfo, filename string) (string, error) {
	if info.invalidID != "" {
		return "", fmt.Errorf("invalid global MapId %q; use -map", info.invalidID)
	}
	if len(info.mapIDs) > 1 {
		return "", errors.New("conflicting global MapId values; use -map")
	}
	if len(info.levels) > 1 {
		return "", errors.New("conflicting source level values; use -map")
	}
	mapID, levelID := one(info.mapIDs), one(info.levels)
	if mapID != "" && levelID != "" && mapID != levelID {
		return "", errors.New("global MapId and source level disagree; use -map")
	}
	if mapID != "" {
		return mapID, nil
	}
	if levelID != "" {
		return levelID, nil
	}
	if id, ok := terrainmap.FromFilename(filename); ok {
		return id, nil
	}
	return "", errors.New("map cannot be determined from MapId, source level, or an exact catalog filename token; use -map")
}

func transformACMI(src io.Reader, dst io.Writer, mapID string) (transformInfo, error) {
	var info transformInfo
	anchorLat, anchorLon, ok := terrainmap.AtlasReference(mapID)
	if !ok {
		return info, fmt.Errorf("no Atlas reference for map %q", mapID)
	}

	br := bufio.NewReaderSize(src, 64*1024)
	inTimeline := false
	headerBytes := 0
	eol := "\n"
	lastHadEOL := true
	sawMapID, sawLayout := false, false
	sawLon, sawLat, sawAlt := false, false, false
	wroteGlobals := false
	injectGlobals := func() error {
		if wroteGlobals {
			return nil
		}
		var props []string
		if !sawMapID {
			props = append(props, "MapId="+terrainmap.FormatAtlasMapID(mapID))
		}
		if !sawLayout {
			props = append(props, "WT_TerrainLayout="+layoutValue)
		}
		if !sawLon {
			props = append(props, "ReferenceLongitude=0")
		}
		if !sawLat {
			props = append(props, "ReferenceLatitude=0")
		}
		if !sawAlt {
			props = append(props, "ReferenceAltitude=0")
		}
		if len(props) == 0 {
			wroteGlobals = true
			return nil
		}
		line := "0," + strings.Join(props, ",") + eol
		if headerBytes+len(line) > maxLineBytes {
			return fmt.Errorf("ACMI header would exceed %d bytes", maxLineBytes)
		}
		if !lastHadEOL {
			line = eol + line
		}
		if _, err := io.WriteString(dst, line); err != nil {
			return err
		}
		headerBytes += len(line)
		info.inserted += len(props)
		wroteGlobals = true
		lastHadEOL = true
		return nil
	}

	for {
		raw, err := readLine(br)
		if len(raw) > 0 {
			content := trimEOL(raw)
			lineEOL := raw[len(content):]
			if lineEOL != "" {
				lastHadEOL = true
				if !inTimeline {
					eol = lineEOL
				}
			} else {
				lastHadEOL = false
			}
			clean := strings.TrimSpace(stripBOM(content))
			if !inTimeline && strings.HasPrefix(clean, "#") {
				if err := injectGlobals(); err != nil {
					return info, err
				}
				inTimeline = true
			}
			if !inTimeline {
				headerBytes += len(raw)
				if headerBytes > maxLineBytes {
					return info, fmt.Errorf("ACMI header exceeds %d bytes", maxLineBytes)
				}
			}

			fields := splitProperties(content)
			changed := false
			global := len(fields) > 1 && strings.TrimSpace(stripBOM(fields[0])) == "0"
			for i, field := range fields {
				name, value, ok := property(field)
				if !ok {
					continue
				}
				if name == "T" {
					newValue, err := migrateT(value, anchorLat, anchorLon)
					if err != nil {
						return info, err
					}
					if newValue != value {
						fields[i] = "T=" + newValue
						changed = true
					}
					info.transformed++
					continue
				}
				if !global {
					continue
				}
				want := ""
				switch name {
				case "MapId":
					sawMapID = true
					want = "MapId=" + terrainmap.FormatAtlasMapID(mapID)
				case "WT_TerrainLayout":
					sawLayout = true
					want = "WT_TerrainLayout=" + layoutValue
				case "ReferenceLongitude":
					sawLon = true
					want = "ReferenceLongitude=0"
				case "ReferenceLatitude":
					sawLat = true
					want = "ReferenceLatitude=0"
				case "ReferenceAltitude":
					sawAlt = true
					want = "ReferenceAltitude=0"
				}
				if want != "" {
					info.globals++
					if field != want {
						fields[i] = want
						changed = true
					}
				}
			}
			out := raw
			if changed {
				outContent := strings.Join(fields, ",")
				if len(outContent) > maxLineBytes {
					return info, fmt.Errorf("ACMI line would exceed %d bytes", maxLineBytes)
				}
				out = outContent + lineEOL
			}
			if !inTimeline {
				headerBytes += len(out) - len(raw)
				if headerBytes > maxLineBytes {
					return info, fmt.Errorf("ACMI header would exceed %d bytes", maxLineBytes)
				}
			}
			if out != "" {
				if _, err := io.WriteString(dst, out); err != nil {
					return info, err
				}
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return info, err
		}
	}
	if err := injectGlobals(); err != nil {
		return info, err
	}
	return info, nil
}

func validateT(value string) error {
	parts := strings.Split(value, "|")
	if len(parts) != 5 && len(parts) != 8 {
		return fmt.Errorf("unsupported T format with %d fields; expected T5 (UV) or T8 (roll/pitch/yaw+UV)", len(parts))
	}
	for _, part := range parts {
		v, err := strconv.ParseFloat(part, 64)
		if err != nil || !finite(v) {
			return fmt.Errorf("T contains a non-finite or invalid number %q", part)
		}
	}
	return nil
}

func migrateT(value string, anchorLat, anchorLon float64) (string, error) {
	if err := validateT(value); err != nil {
		return "", err
	}
	parts := strings.Split(value, "|")
	u, _ := strconv.ParseFloat(parts[len(parts)-2], 64)
	v, _ := strconv.ParseFloat(parts[len(parts)-1], 64)
	lat := anchorLat + v/111320
	cosLat := math.Cos(lat * math.Pi / 180)
	if math.Abs(cosLat) < 1e-15 {
		return "", errors.New("latitude is too close to a pole for Atlas migration")
	}
	lon := anchorLon + u/(111320*cosLat)
	if !finite(lat) || !finite(lon) {
		return "", errors.New("Atlas migration produced a non-finite coordinate")
	}
	parts[0] = strconv.FormatFloat(lon, 'f', 7, 64)
	parts[1] = strconv.FormatFloat(lat, 'f', 7, 64)
	return strings.Join(parts, "|"), nil
}

func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

func readAndTransform(input, output, explicitMap string, overwrite bool) (transformInfo, string, error) {
	var info transformInfo
	if !strings.HasSuffix(strings.ToLower(input), ".acmi") {
		return info, "", errors.New("input is not a text .acmi file")
	}
	if strings.HasSuffix(strings.ToLower(input), ".zip.acmi") {
		return info, "", errors.New("zip-compressed .zip.acmi is unsupported; extract it to a text .acmi first")
	}
	f, err := os.Open(input)
	if err != nil {
		return info, "", err
	}
	head := make([]byte, 4)
	n, _ := io.ReadFull(f, head)
	_ = f.Close()
	if n == 4 && isZipMagic(head) {
		return info, "", errors.New("zip-compressed .acmi is unsupported; extract it to a text .acmi first")
	}

	f, err = os.Open(input)
	if err != nil {
		return info, "", err
	}
	scan, scanErr := scanACMI(f)
	closeErr := f.Close()
	if scanErr != nil {
		return info, "", scanErr
	}
	if closeErr != nil {
		return info, "", closeErr
	}
	mapID := ""
	if explicitMap != "" {
		mapID, err = terrainmap.NormalizeMapID(explicitMap)
		if err != nil {
			return info, "", err
		}
	} else {
		mapID, err = chooseMapID(scan, input)
		if err != nil {
			return info, "", err
		}
	}
	if _, _, ok := terrainmap.AtlasReference(mapID); !ok {
		return info, "", fmt.Errorf("no Atlas reference for map %q", mapID)
	}
	if output == "" {
		output = defaultOutput(input)
	}
	same := samePath(input, output)
	if exists, statErr := pathExists(output); statErr != nil {
		return info, "", statErr
	} else if exists && !overwrite {
		return info, "", fmt.Errorf("output already exists: %s (use -overwrite)", output)
	}
	if same && !overwrite {
		return info, "", fmt.Errorf("output already exists: %s (use -overwrite)", output)
	}
	if err := os.MkdirAll(filepath.Dir(output), 0755); err != nil {
		return info, "", err
	}

	tmp, err := os.CreateTemp(filepath.Dir(output), ".acmi-atlas-*")
	if err != nil {
		return info, "", err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	src, err := os.Open(input)
	if err != nil {
		_ = tmp.Close()
		return info, "", err
	}
	bw := bufio.NewWriterSize(tmp, 128*1024)
	info, err = transformACMI(src, bw, mapID)
	if err == nil {
		err = bw.Flush()
	}
	if closeSrcErr := src.Close(); err == nil {
		err = closeSrcErr
	}
	if err == nil {
		err = tmp.Sync()
	}
	if closeTmpErr := tmp.Close(); err == nil {
		err = closeTmpErr
	}
	if err != nil {
		return info, "", err
	}
	if same {
		equal, err := sameContents(tmpName, input)
		if err != nil {
			return info, "", err
		}
		if equal {
			return info, mapID, nil
		}
	}
	if err := installOutput(tmpName, output, overwrite); err != nil {
		return info, "", err
	}
	return info, mapID, nil
}

func installOutput(tmpName, output string, overwrite bool) error {
	if !overwrite {
		dst, err := os.OpenFile(output, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
		if err != nil {
			return err
		}
		src, err := os.Open(tmpName)
		if err == nil {
			_, err = io.Copy(dst, src)
			_ = src.Close()
		}
		closeErr := dst.Close()
		if err == nil {
			err = closeErr
		}
		if err != nil {
			_ = os.Remove(output)
		}
		return err
	}
	if err := os.Chmod(tmpName, 0644); err != nil {
		return err
	}
	return os.Rename(tmpName, output)
}

func splitProperties(s string) []string {
	fields := make([]string, 0, 8)
	start := 0
	escaped := false
	for i := 0; i < len(s); i++ {
		if escaped {
			escaped = false
			continue
		}
		switch s[i] {
		case '\\':
			escaped = true
		case ',':
			fields = append(fields, s[start:i])
			start = i + 1
		}
	}
	return append(fields, s[start:])
}

func property(field string) (name, value string, ok bool) {
	i := strings.IndexByte(field, '=')
	if i <= 0 {
		return "", "", false
	}
	return field[:i], field[i+1:], true
}

func trimEOL(s string) string {
	s = strings.TrimSuffix(s, "\n")
	return strings.TrimSuffix(s, "\r")
}

func stripBOM(s string) string { return strings.TrimPrefix(s, "\ufeff") }

func readLine(br *bufio.Reader) (string, error) {
	var line []byte
	for {
		fragment, err := br.ReadSlice('\n')
		line = append(line, fragment...)
		contentLen := len(line)
		if contentLen > 0 && line[contentLen-1] == '\n' {
			contentLen--
			if contentLen > 0 && line[contentLen-1] == '\r' {
				contentLen--
			}
		}
		if contentLen > maxLineBytes {
			return "", fmt.Errorf("ACMI line exceeds %d bytes", maxLineBytes)
		}
		if err == nil {
			return string(line), nil
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		return string(line), err
	}
}

func one(set map[string]struct{}) string {
	for value := range set {
		return value
	}
	return ""
}

func isZipMagic(b []byte) bool {
	return bytes.Equal(b, []byte{'P', 'K', 3, 4}) ||
		bytes.Equal(b, []byte{'P', 'K', 5, 6}) ||
		bytes.Equal(b, []byte{'P', 'K', 7, 8})
}

func samePath(a, b string) bool {
	aa, errA := filepath.Abs(a)
	bb, errB := filepath.Abs(b)
	if errA != nil || errB != nil {
		return filepath.Clean(a) == filepath.Clean(b)
	}
	return strings.EqualFold(filepath.Clean(aa), filepath.Clean(bb))
}

func sameContents(a, b string) (bool, error) {
	fa, err := os.Open(a)
	if err != nil {
		return false, err
	}
	defer fa.Close()
	fb, err := os.Open(b)
	if err != nil {
		return false, err
	}
	defer fb.Close()
	bufA := make([]byte, 64*1024)
	bufB := make([]byte, 64*1024)
	for {
		na, errA := io.ReadFull(fa, bufA)
		nb, errB := io.ReadFull(fb, bufB)
		if na != nb || !bytes.Equal(bufA[:na], bufB[:nb]) {
			return false, nil
		}
		aDone := errors.Is(errA, io.EOF) || errors.Is(errA, io.ErrUnexpectedEOF)
		bDone := errors.Is(errB, io.EOF) || errors.Is(errB, io.ErrUnexpectedEOF)
		if aDone || bDone {
			return aDone && bDone, nil
		}
		if errA != nil {
			return false, errA
		}
		if errB != nil {
			return false, errB
		}
	}
}
