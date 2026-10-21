package main

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl"
)

const replayDirectoryHashDomain = "wrpl-inspector:server-replay-directory:v1"

// replayInput owns the resources used by either a single replay or an
// already-preflighted server replay directory. Directory packet streams are
// opened lazily by wrpl and share one reader when consumed by the exporter.
type replayInput struct {
	path      string
	reader    *wrpl.ReplayReader
	directory *wrpl.ReplayDirectoryInfo
	file      *os.File
}

func (in *replayInput) isDirectory() bool {
	return in != nil && in.directory != nil
}

func (in *replayInput) Close() error {
	if in == nil {
		return nil
	}
	var closeErrors []error
	if in.reader != nil {
		if err := in.reader.Close(); err != nil {
			closeErrors = append(closeErrors, err)
		}
		in.reader = nil
	}
	if in.file != nil {
		if err := in.file.Close(); err != nil {
			closeErrors = append(closeErrors, err)
		}
		in.file = nil
	}
	return errors.Join(closeErrors...)
}

func openReplayInput(path string) (*replayInput, error) {
	return openReplayInputWithOptions(path, true)
}

func openReplayInputWithOptions(path string, allowIncomplete bool) (*replayInput, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("reading input %q: %w", path, err)
	}
	if info.IsDir() {
		reader, directory, err := wrpl.OpenReplayDirectoryWithOptions(path, wrpl.ReplayDirectoryOptions{
			AllowIncomplete: allowIncomplete,
		})
		if err != nil {
			if reader != nil {
				_ = reader.Close()
			}
			return nil, fmt.Errorf("preflighting replay directory %q: %w", path, err)
		}
		if reader == nil || directory == nil {
			if reader != nil {
				_ = reader.Close()
			}
			return nil, fmt.Errorf("preflighting replay directory %q: missing replay or directory information", path)
		}
		if len(directory.PartNumbers) == 0 || len(directory.PartNumbers) != len(directory.Paths) {
			_ = reader.Close()
			return nil, fmt.Errorf("preflighting replay directory %q: inconsistent part and path metadata", path)
		}
		return &replayInput{path: path, reader: reader, directory: directory}, nil
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("input %q is not a regular replay file or directory", path)
	}

	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("opening replay %q: %w", path, err)
	}
	reader, err := wrpl.OpenReplay(f, true, true, true)
	if err != nil {
		if reader != nil {
			_ = reader.Close()
		}
		_ = f.Close()
		return nil, fmt.Errorf("opening replay %q: %w", path, err)
	}
	if reader.Header.IsServer() {
		_ = reader.Close()
		_ = f.Close()
		return nil, fmt.Errorf("input %q is one server replay part (part %d); only one shard would be exported. Pass the shard directory instead (只导出一片，请传目录)",
			path, reader.Header.ReplayPartNumber)
	}
	return &replayInput{path: path, reader: reader, file: f}, nil
}

func resolveCLIInput(flagInput string, positional []string) (string, error) {
	if flagInput != "" && len(positional) > 0 {
		return "", errors.New("input specified twice: use either -in or one positional replay path, not both")
	}
	if len(positional) > 1 {
		return "", fmt.Errorf("multiple positional inputs are not supported; provide one replay file or server replay shard directory (got %d)", len(positional))
	}
	if flagInput != "" {
		return flagInput, nil
	}
	if len(positional) == 1 {
		return positional[0], nil
	}
	return "", nil
}

func defaultACMIOutputName(input string, directory bool) string {
	return defaultACMIOutputNameForState(input, directory, false)
}

func defaultACMIOutputNameForState(input string, directory, incomplete bool) string {
	suffix := ".acmi"
	if directory && incomplete {
		suffix = ".partial.acmi"
	}
	if directory {
		base := filepath.Base(filepath.Clean(input))
		if base == "" || base == "." || strings.Trim(base, `/\`) == "" {
			base = "replay"
		}
		return base + suffix
	}
	ext := filepath.Ext(input)
	return filepath.Base(strings.TrimSuffix(input, ext)) + ".acmi"
}

func checkOutputDoesNotOverwriteInput(output string, inputPaths []string) error {
	outputAbs, err := filepath.Abs(output)
	if err != nil {
		return fmt.Errorf("resolving output path %q: %w", output, err)
	}
	outputAbs = filepath.Clean(outputAbs)
	outputInfo, outputStatErr := os.Stat(outputAbs)
	for _, input := range inputPaths {
		inputAbs, err := filepath.Abs(input)
		if err != nil {
			return fmt.Errorf("resolving input path %q: %w", input, err)
		}
		inputAbs = filepath.Clean(inputAbs)
		if strings.EqualFold(outputAbs, inputAbs) {
			return fmt.Errorf("output path %q is also an input replay path; choose a different -out", output)
		}
		if outputStatErr == nil {
			if inputInfo, err := os.Stat(inputAbs); err == nil && os.SameFile(outputInfo, inputInfo) {
				return fmt.Errorf("output path %q is the same file as input %q; choose a different -out", output, input)
			}
		}
	}
	return nil
}

type replayDirectoryPart struct {
	part    byte
	size    int64
	content [sha256.Size]byte
}

// hashReplayDirectory identifies a directory by the ordered set of physical
// part numbers and their content digests. The domain prefix prevents this
// selector from ever matching a table keyed by a single replay's raw SHA-256.
func hashReplayDirectory(info *wrpl.ReplayDirectoryInfo) (string, error) {
	if info == nil {
		return "", errors.New("replay directory information is missing")
	}
	if len(info.PartNumbers) == 0 || len(info.PartNumbers) != len(info.Paths) {
		return "", errors.New("replay directory part metadata is empty or inconsistent")
	}
	parts := make([]replayDirectoryPart, 0, len(info.PartNumbers))
	seen := make(map[byte]bool, len(info.PartNumbers))
	for i, part := range info.PartNumbers {
		if seen[part] {
			return "", fmt.Errorf("replay directory contains duplicate part %d", part)
		}
		seen[part] = true
		f, err := os.Open(info.Paths[i])
		if err != nil {
			return "", fmt.Errorf("hashing replay part %d: %w", part, err)
		}
		stat, statErr := f.Stat()
		if statErr != nil {
			_ = f.Close()
			return "", fmt.Errorf("stating replay part %d: %w", part, statErr)
		}
		contentHash := sha256.New()
		written, copyErr := io.Copy(contentHash, f)
		closeErr := f.Close()
		if copyErr != nil {
			return "", fmt.Errorf("hashing replay part %d: %w", part, copyErr)
		}
		if closeErr != nil {
			return "", fmt.Errorf("closing replay part %d after hashing: %w", part, closeErr)
		}
		if written != stat.Size() {
			return "", fmt.Errorf("replay part %d changed while hashing (read %d of %d bytes)", part, written, stat.Size())
		}
		var content [sha256.Size]byte
		copy(content[:], contentHash.Sum(nil))
		parts = append(parts, replayDirectoryPart{part: part, size: written, content: content})
	}
	sort.Slice(parts, func(i, j int) bool { return parts[i].part < parts[j].part })

	h := sha256.New()
	_, _ = io.WriteString(h, replayDirectoryHashDomain+"\x00")
	var count [4]byte
	binary.LittleEndian.PutUint32(count[:], uint32(len(parts)))
	_, _ = h.Write(count[:])
	for _, part := range parts {
		var size [8]byte
		binary.LittleEndian.PutUint64(size[:], uint64(part.size))
		_, _ = h.Write([]byte{part.part})
		_, _ = h.Write(size[:])
		_, _ = h.Write(part.content[:])
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

type acmiSourceContext struct {
	directory *wrpl.ReplayDirectoryInfo
}

func (source acmiSourceContext) properties() []string {
	if source.directory == nil {
		return nil
	}
	complete := "1"
	if source.directory.Incomplete {
		complete = "0"
	}
	properties := []string{
		"WT_InputKind=ServerReplayDirectory",
		fmt.Sprintf("WT_ServerSessionID=%016x", source.directory.SessionID),
		fmt.Sprintf("WT_ReplayPartCount=%d", len(source.directory.PartNumbers)),
		"WT_InputComplete=" + complete,
	}
	if source.directory.Incomplete {
		properties = append(properties,
			"WT_IncompleteReason="+escapeACMIValue(replayIncompleteReason(source.directory)),
			"Briefing=Incomplete server replay: missing final segment",
		)
	}
	return properties
}

func replayIncompleteReason(info *wrpl.ReplayDirectoryInfo) string {
	if info != nil {
		if reason := strings.TrimSpace(info.CompletenessNote); reason != "" {
			return reason
		}
	}
	return "missing final segment"
}
