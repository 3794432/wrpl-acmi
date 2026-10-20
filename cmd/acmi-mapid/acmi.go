package main

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/maxsupermanhd/wrpl-inspector/v3/internal/terrainmap"
)

const maxHeaderBytes = 1 << 20

type scanInfo struct {
	mapIDs map[string]struct{}
	levels map[string]struct{}
}

type transformInfo struct {
	inserted bool
	rewrote  int
	dropped  int
	time     int
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
			if !inTimeline && strings.HasPrefix(strings.TrimSpace(stripBOM(content)), "#") {
				inTimeline = true
			}
			if !inTimeline {
				headerBytes += len(raw)
				if headerBytes > maxHeaderBytes {
					return info, fmt.Errorf("ACMI header exceeds %d bytes", maxHeaderBytes)
				}
				if !sawType && strings.TrimSpace(stripBOM(content)) != "" {
					if strings.TrimSpace(stripBOM(content)) != "FileType=text/acmi/tacview" {
						return info, errors.New("not a text .acmi file (expected FileType=text/acmi/tacview)")
					}
					sawType = true
				}
			} else if strings.HasPrefix(strings.TrimSpace(content), "#") {
				continue
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
						}
					case "SourceLevel", "Level", "Source", "Location":
						if !inTimeline {
							if id, ok := terrainmap.FromLevel(value); ok {
								info.levels[id] = struct{}{}
							}
						}
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
	return info, nil
}

func chooseMapID(info scanInfo, filename string) (string, error) {
	if len(info.mapIDs) > 1 {
		return "", errors.New("conflicting known MapId values; use -map")
	}
	if len(info.levels) > 1 {
		return "", errors.New("conflicting known source levels; use -map")
	}
	mapID := one(info.mapIDs)
	levelID := one(info.levels)
	if mapID != "" && levelID != "" && mapID != levelID {
		return "", errors.New("MapId and source level disagree; use -map")
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
	br := bufio.NewReaderSize(src, 64*1024)
	inTimeline := false
	headerBytes := 0
	headerIDKept := false
	eol := "\n"
	endsWithNewline := true
	timelineTime := 0.0
	for {
		raw, err := readLine(br)
		if len(raw) > 0 {
			endsWithNewline = strings.HasSuffix(raw, "\n")
			content := trimEOL(raw)
			if !inTimeline && strings.HasPrefix(strings.TrimSpace(stripBOM(content)), "#") {
				inTimeline = true
				if !headerIDKept {
					line := "0,MapId=" + terrainmap.FormatMapID(mapID) + eol
					if headerBytes+len(line) > maxHeaderBytes {
						return info, fmt.Errorf("ACMI header would exceed %d bytes", maxHeaderBytes)
					}
					if _, werr := io.WriteString(dst, line); werr != nil {
						return info, werr
					}
					info.inserted = true
					headerIDKept = true
				}
			}
			if inTimeline && strings.HasPrefix(strings.TrimSpace(content), "#") {
				timelineTime, _ = strconv.ParseFloat(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(content), "#")), 64)
			}
			if !inTimeline {
				headerBytes += len(raw)
				if headerBytes > maxHeaderBytes {
					return info, fmt.Errorf("ACMI header exceeds %d bytes", maxHeaderBytes)
				}
				if strings.HasSuffix(raw, "\r\n") {
					eol = "\r\n"
				}
			}
			out := raw
			if fields := splitProperties(content); len(fields) > 1 && strings.TrimSpace(stripBOM(fields[0])) == "0" {
				var present bool
				var rewrote, dropped int
				out, present, rewrote, dropped = rewriteMapID(raw, mapID, inTimeline || !headerIDKept)
				info.rewrote += rewrote
				info.dropped += dropped
				if present && !inTimeline {
					headerIDKept = true
				}
				if present && inTimeline && timelineTime > 0 {
					info.time++
				}
			}
			if out != "" {
				if _, werr := io.WriteString(dst, out); werr != nil {
					return info, werr
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
	if !headerIDKept {
		prefix := ""
		if !endsWithNewline {
			prefix = eol
		}
		line := prefix + "0,MapId=" + terrainmap.FormatMapID(mapID) + eol
		if headerBytes+len(line) > maxHeaderBytes {
			return info, fmt.Errorf("ACMI header would exceed %d bytes", maxHeaderBytes)
		}
		if _, err := io.WriteString(dst, line); err != nil {
			return info, err
		}
		info.inserted = true
	}
	return info, nil
}

func rewriteMapID(raw, mapID string, keepFirst bool) (out string, present bool, rewrote, dropped int) {
	content := trimEOL(raw)
	eol := raw[len(content):]
	fields := splitProperties(content)
	kept := make([]string, 0, len(fields))
	for _, field := range fields {
		name, _, ok := property(field)
		if ok && name == "MapId" {
			present = true
			if !keepFirst {
				dropped++
				continue
			}
			keepFirst = false
			want := "MapId=" + terrainmap.FormatMapID(mapID)
			if field != want {
				rewrote++
			}
			field = want
		}
		kept = append(kept, field)
	}
	if dropped > 0 || rewrote > 0 {
		out = strings.Join(kept, ",") + eol
	} else {
		out = raw
	}
	return out, present, rewrote, dropped
}

// splitProperties splits only unescaped commas. Backslash escapes are kept
// byte-for-byte, so unrelated attributes and timeline lines remain unchanged.
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

// readLine uses ReadSlice so a file with no newline cannot force an unbounded
// allocation. Fragments are checked before they are appended to the line.
func readLine(br *bufio.Reader) (string, error) {
	var line []byte
	for {
		fragment, err := br.ReadSlice('\n')
		contentLen := len(line) + len(fragment)
		if len(fragment) > 0 && fragment[len(fragment)-1] == '\n' {
			contentLen--
			if contentLen > 0 && fragment[len(fragment)-2] == '\r' {
				contentLen--
			}
		}
		if contentLen > maxHeaderBytes {
			return "", fmt.Errorf("ACMI line exceeds %d bytes", maxHeaderBytes)
		}
		line = append(line, fragment...)
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

func readAndTransform(input, output, explicitMap string, overwrite bool) (transformInfo, string, error) {
	var info transformInfo
	lower := strings.ToLower(input)
	if !strings.HasSuffix(lower, ".acmi") {
		return info, "", errors.New("input is not a text .acmi file")
	}
	if strings.HasSuffix(lower, ".zip.acmi") {
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
	scan, err := scanACMI(f)
	_ = f.Close()
	if err != nil {
		return info, "", err
	}
	mapID := explicitMap
	if mapID == "" {
		mapID, err = chooseMapID(scan, input)
		if err != nil {
			return info, "", err
		}
	}
	if output == "" {
		output = defaultOutput(input)
	}
	if output != input {
		if _, statErr := os.Stat(output); statErr == nil && !overwrite {
			return info, "", fmt.Errorf("output already exists: %s (use -overwrite)", output)
		} else if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
			return info, "", statErr
		}
	} else if !overwrite {
		return info, "", fmt.Errorf("output already exists: %s (use -overwrite)", output)
	}
	if err := os.MkdirAll(filepath.Dir(output), 0755); err != nil {
		return info, "", err
	}
	tmp, err := os.CreateTemp(filepath.Dir(output), ".acmi-mapid-*")
	if err != nil {
		return info, "", err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	f, err = os.Open(input)
	if err != nil {
		_ = tmp.Close()
		return info, "", err
	}
	bw := bufio.NewWriterSize(tmp, 128*1024)
	info, err = transformACMI(f, bw, mapID)
	closeErr := f.Close()
	if err == nil {
		err = bw.Flush()
	}
	if err == nil {
		err = closeErr
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
	if output == input {
		same, err := sameContents(tmpName, input)
		if err != nil {
			return info, "", err
		}
		if same {
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
	// Temp files live beside the output. Go uses MoveFileExW with
	// MOVEFILE_REPLACE_EXISTING on Windows: a failed rename leaves the old
	// output intact. Never delete it first or fall back to a partial copy.
	if err := os.Chmod(tmpName, 0644); err != nil {
		return err
	}
	return os.Rename(tmpName, output)
}

func defaultOutput(input string) string {
	if strings.HasSuffix(strings.ToLower(input), ".mapid.acmi") {
		return input
	}
	return strings.TrimSuffix(input, filepath.Ext(input)) + ".mapid.acmi"
}

func isZipMagic(b []byte) bool {
	return bytes.Equal(b, []byte{'P', 'K', 3, 4}) ||
		bytes.Equal(b, []byte{'P', 'K', 5, 6}) ||
		bytes.Equal(b, []byte{'P', 'K', 7, 8})
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
