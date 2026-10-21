package main

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl"
)

func TestResolveCLIInputRejectsAmbiguousArguments(t *testing.T) {
	if _, err := resolveCLIInput("a.wrpl", []string{"b.wrpl"}); err == nil ||
		!strings.Contains(err.Error(), "either -in or one positional") {
		t.Fatalf("expected -in plus positional input to be rejected, got %v", err)
	}
	if _, err := resolveCLIInput("", []string{"a.wrpl", "b.wrpl"}); err == nil ||
		!strings.Contains(err.Error(), "multiple positional inputs") {
		t.Fatalf("expected multiple positional inputs to be rejected, got %v", err)
	}
	if got, err := resolveCLIInput("", []string{"folder with spaces"}); err != nil || got != "folder with spaces" {
		t.Fatalf("single positional input: got %q, %v", got, err)
	}
}

func TestDefaultACMIOutputNameForDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "replay folder")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if got, want := defaultACMIOutputName(dir+string(filepath.Separator), true), "replay folder.acmi"; got != want {
		t.Fatalf("trailing separator output name: got %q, want %q", got, want)
	}
	root := filepath.VolumeName(dir) + string(filepath.Separator)
	if got, want := defaultACMIOutputName(root, true), "replay.acmi"; got != want {
		t.Fatalf("root fallback output name: got %q, want %q", got, want)
	}
	if got, want := defaultACMIOutputNameForState(dir, true, true), "replay folder.partial.acmi"; got != want {
		t.Fatalf("incomplete directory output name: got %q, want %q", got, want)
	}
	if got, want := defaultACMIOutputName(filepath.Join(dir, "client name.wrpl"), false), "client name.acmi"; got != want {
		t.Fatalf("single-file output name changed: got %q, want %q", got, want)
	}
}

func TestReplayDirectoryHashIgnoresNamesAndPathOrder(t *testing.T) {
	dir := t.TempDir()
	files := map[byte]string{
		0: "part zero content",
		1: "part one content",
		3: "part three content",
	}
	paths := make(map[byte]string, len(files))
	for part, content := range files {
		path := filepath.Join(dir, "original-name-"+string(rune('a'+part))+".wrpl")
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
		paths[part] = path
	}
	firstInfo := &wrpl.ReplayDirectoryInfo{
		SessionID:   0x4ad212e800569482,
		PartNumbers: []byte{0, 1, 3},
		Paths:       []string{paths[0], paths[1], paths[3]},
	}
	first, err := hashReplayDirectory(firstInfo)
	if err != nil {
		t.Fatal(err)
	}

	for part, oldPath := range paths {
		newPath := filepath.Join(dir, "renamed-"+string(rune('z'-part))+".replay")
		if err := os.Rename(oldPath, newPath); err != nil {
			t.Fatal(err)
		}
		paths[part] = newPath
	}
	secondInfo := &wrpl.ReplayDirectoryInfo{
		SessionID:   firstInfo.SessionID,
		PartNumbers: []byte{3, 1, 0},
		Paths:       []string{paths[3], paths[1], paths[0]},
	}
	second, err := hashReplayDirectory(secondInfo)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("directory digest changed after rename/reorder: %s != %s", first, second)
	}

	partZeroSHA, err := hashMissileGuidanceReplay(paths[0])
	if err != nil {
		t.Fatal(err)
	}
	if first == partZeroSHA {
		t.Fatal("directory digest reused the part 0 single-file SHA-256")
	}

	if err := os.WriteFile(paths[1], []byte("changed part one content"), 0644); err != nil {
		t.Fatal(err)
	}
	third, err := hashReplayDirectory(secondInfo)
	if err != nil {
		t.Fatal(err)
	}
	if third == second {
		t.Fatal("directory digest did not change after shard content changed")
	}
}

func TestServerSingleReplayIsRejected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server-part-0.wrpl")
	writeSyntheticReplay(t, path, true, 0)
	if _, err := openReplayInput(path); err == nil ||
		!strings.Contains(err.Error(), "只导出一片，请传目录") {
		t.Fatalf("expected a server shard to be rejected with directory guidance, got %v", err)
	}
	if _, err := openReplayInputWithOptions(path, true); err == nil ||
		!strings.Contains(err.Error(), "server replay part") {
		t.Fatalf("expected a server shard to require a directory, got %v", err)
	}
}

func TestIncompleteDirectoryExportsAvailablePartsByDefault(t *testing.T) {
	if got := flag.Lookup("allow-incomplete").DefValue; got != "true" {
		t.Fatalf("CLI must allow a missing tail by default, got %s", got)
	}
	dir := t.TempDir()
	writeSyntheticReplayPackets(t, filepath.Join(dir, "0000.wrpl"), true, 0, []byte{0x82, 0x15, 0x00})
	writeSyntheticReplayPackets(t, filepath.Join(dir, "0001.wrpl"), true, 1, []byte{0x82, 0x15, 0x00})
	if input, err := openReplayInputWithOptions(dir, false); err == nil || !strings.Contains(err.Error(), "final replay part is missing") {
		if input != nil {
			input.Close()
		}
		t.Fatalf("strict directory open did not reject missing final segment: %v", err)
	}
	input, err := openReplayInput(dir)
	if err != nil {
		t.Fatalf("default partial replay open failed: %v", err)
	}
	defer input.Close()
	if input.directory == nil || !input.directory.Incomplete {
		t.Fatalf("incomplete directory was not marked partial: %+v", input.directory)
	}
	if input.directory.CompletenessNote == "" {
		t.Fatal("incomplete directory has no completeness note")
	}
}

func TestClientSingleReplayStillOpens(t *testing.T) {
	path := filepath.Join(t.TempDir(), "client.wrpl")
	writeSyntheticReplay(t, path, false, 0)
	input, err := openReplayInput(path)
	if err != nil {
		t.Fatalf("opening client replay: %v", err)
	}
	defer input.Close()
	if input.isDirectory() {
		t.Fatal("client replay was classified as a directory")
	}
}

func TestBadShardPreflightDoesNotCreateOutput(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "0000.wrpl"), []byte("not a replay"), 0644); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "must-not-exist.acmi")
	if input, err := openReplayInput(dir); err == nil {
		input.Close()
		t.Fatal("bad shard directory unexpectedly passed preflight")
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatalf("output was created after failed preflight: %v", err)
	}
}

func TestDirectorySourcePropertiesAreExplicit(t *testing.T) {
	info := &wrpl.ReplayDirectoryInfo{
		SessionID:   0x4ad212e800569482,
		PartNumbers: []byte{0, 1, 3},
		Paths:       []string{"a", "b", "c"},
	}
	path := filepath.Join(t.TempDir(), "directory.acmi")
	if err := writeACMIWithEventsMapIDAndSource(path, wrpl.WRPLHeader{}, nil, nil, nil, nil, "", acmiSourceContext{directory: info}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"0,WT_InputKind=ServerReplayDirectory\n",
		"0,WT_ServerSessionID=4ad212e800569482\n",
		"0,WT_ReplayPartCount=3\n",
		"0,WT_InputComplete=1\n",
	} {
		if !bytes.Contains(data, []byte(want)) {
			t.Fatalf("directory ACMI is missing %q:\n%s", want, data)
		}
	}
}

func TestSingleFileWriterOmitsDirectoryProperties(t *testing.T) {
	path := filepath.Join(t.TempDir(), "single.acmi")
	if err := writeACMIWithEventsMapID(path, wrpl.WRPLHeader{}, nil, nil, nil, nil, ""); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte("WT_InputKind=")) || bytes.Contains(data, []byte("WT_InputComplete=")) {
		t.Fatalf("single-file output contains directory-only metadata:\n%s", data)
	}
}

func TestIncompleteDirectorySourceIsExplicit(t *testing.T) {
	info := &wrpl.ReplayDirectoryInfo{
		SessionID:        0x4ad212e800569482,
		PartNumbers:      []byte{0, 1},
		Paths:            []string{"a", "b"},
		Incomplete:       true,
		CompletenessNote: "missing final segment",
	}
	path := filepath.Join(t.TempDir(), "partial.acmi")
	if err := writeACMIWithEventsMapIDAndSource(path, wrpl.WRPLHeader{}, nil, nil, nil, nil, "", acmiSourceContext{directory: info}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"0,WT_InputComplete=0\n",
		"0,WT_IncompleteReason=missing final segment\n",
		"0,Briefing=Incomplete server replay: missing final segment\n",
	} {
		if !bytes.Contains(data, []byte(want)) {
			t.Fatalf("partial ACMI is missing %q:\n%s", want, data)
		}
	}
}

func TestOutputCannotAliasInput(t *testing.T) {
	path := filepath.Join(t.TempDir(), "replay.wrpl")
	if err := os.WriteFile(path, []byte("input"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := checkOutputDoesNotOverwriteInput(path, []string{path}); err == nil {
		t.Fatal("output equal to input was accepted")
	}
	if err := checkOutputDoesNotOverwriteInput(filepath.Join(filepath.Dir(path), "other.acmi"), []string{path}); err != nil {
		t.Fatalf("distinct output rejected: %v", err)
	}
}

func writeSyntheticReplay(t *testing.T, path string, server bool, part byte) {
	writeSyntheticReplayPackets(t, path, server, part, nil)
}

func writeSyntheticReplayPackets(t *testing.T, path string, server bool, part byte, packets []byte) {
	t.Helper()
	header := wrpl.WRPLHeader{SessionID: 0x4ad212e800569482, ReplayPartNumber: part}
	copy(header.Magic[:], []byte{0xe5, 0xac, 0x00, 0x10})
	if server {
		header.Raw_Unknown2[1] = 0x5a
	}
	var encoded bytes.Buffer
	if err := binary.Write(&encoded, binary.LittleEndian, header); err != nil {
		t.Fatal(err)
	}
	if encoded.Len() > 1234 {
		t.Fatalf("synthetic header is %d bytes", encoded.Len())
	}
	fileData := make([]byte, 1234, 1234+64)
	copy(fileData, encoded.Bytes())
	var stream bytes.Buffer
	zw := zlib.NewWriter(&stream)
	if len(packets) > 0 {
		if _, err := zw.Write(packets); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	fileData = append(fileData, stream.Bytes()...)
	if err := os.WriteFile(path, fileData, 0644); err != nil {
		t.Fatal(err)
	}
}
