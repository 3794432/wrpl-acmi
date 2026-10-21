package wrpl

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/klauspost/compress/zlib"
	"github.com/klauspost/compress/zstd"
	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet"
)

const testReplaySession uint64 = 0x4ad212e800569482

var testReplayMagic = [4]byte{0xe5, 0xac, 0x00, 0x10}

type testPacketSpec struct {
	packetType byte
	timestamp  *uint32
	payload    []byte
}

func testTimestamp(v uint32) *uint32 {
	return &v
}

func encodeTestPackets(t *testing.T, specs []testPacketSpec) []byte {
	t.Helper()
	var out bytes.Buffer
	for _, spec := range specs {
		headerSize := uint32(2)
		if spec.timestamp != nil {
			headerSize = 6
		}
		packetSize := headerSize + uint32(len(spec.payload))
		if err := packet.WriteVariableLengthSize(&out, packetSize); err != nil {
			t.Fatal(err)
		}
		header := [2]byte{spec.packetType, 0}
		if spec.timestamp == nil {
			header[0] |= 0b00010000
		}
		out.Write(header[:])
		if spec.timestamp != nil {
			if err := binary.Write(&out, binary.LittleEndian, *spec.timestamp); err != nil {
				t.Fatal(err)
			}
		}
		out.Write(spec.payload)
	}
	return out.Bytes()
}

type testReplayConfig struct {
	filename          string
	part              byte
	session           uint64
	version           int32
	level             string
	client            bool
	badMagic          bool
	compression       string
	settings          []byte
	packetStream      []byte
	results           []byte
	sparseResultsSize int64
	truncateBy        int
}

func writeTestReplayPart(t *testing.T, dir string, cfg testReplayConfig) string {
	t.Helper()
	if cfg.filename == "" {
		cfg.filename = fmt.Sprintf("%03d.wrpl", cfg.part)
	}
	if cfg.session == 0 {
		cfg.session = testReplaySession
	}
	if cfg.version == 0 {
		cfg.version = replayVersionZstd
	}
	if cfg.level == "" {
		cfg.level = "levels/test_level.bin"
	}
	if cfg.compression == "" {
		cfg.compression = "zstd"
	}
	if cfg.version != replayVersionZstd && len(cfg.settings) != 0 {
		t.Fatal("legacy test header layout is tested without settings")
	}

	var compressed bytes.Buffer
	switch cfg.compression {
	case "zstd":
		writer, err := zstd.NewWriter(&compressed, zstd.WithEncoderCRC(true))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write(cfg.packetStream); err != nil {
			t.Fatal(err)
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
	case "zlib":
		writer := zlib.NewWriter(&compressed)
		if _, err := writer.Write(cfg.packetStream); err != nil {
			t.Fatal(err)
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
	default:
		t.Fatalf("unknown compression %q", cfg.compression)
	}

	streamOffset := replayHeaderSize + len(cfg.settings)
	resultsOffset := int64(0)
	if len(cfg.results) > 0 || cfg.sparseResultsSize > 0 {
		resultsOffset = int64(streamOffset + compressed.Len())
	}
	headerBytes := encodeTestReplayHeader(t, cfg, resultsOffset)

	var file bytes.Buffer
	file.Write(headerBytes)
	file.Write(cfg.settings)
	file.Write(compressed.Bytes())
	file.Write(cfg.results)
	path := filepath.Join(dir, cfg.filename)
	if err := os.WriteFile(path, file.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	if cfg.sparseResultsSize > 0 {
		size := int64(streamOffset+compressed.Len()) + cfg.sparseResultsSize
		if err := os.Truncate(path, size); err != nil {
			t.Fatal(err)
		}
	}
	if cfg.truncateBy > 0 {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Truncate(path, info.Size()-int64(cfg.truncateBy)); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func encodeTestReplayHeader(t *testing.T, cfg testReplayConfig, resultsOffset int64) []byte {
	t.Helper()
	magic := testReplayMagic
	if cfg.badMagic {
		magic = [4]byte{0, 1, 2, 3}
	}
	header := WRPLHeader{
		Magic:            magic,
		Version:          cfg.version,
		ResultsBlkOffset: int32(resultsOffset),
		SessionID:        cfg.session,
		ReplayPartNumber: cfg.part,
		SettingsBLKSize:  uint16(len(cfg.settings)),
	}
	copy(header.Raw_Level[:], cfg.level)
	if !cfg.client {
		header.Raw_Unknown2[1] = 0x5a
	}
	var buf bytes.Buffer
	if err := binary.Write(&buf, binary.LittleEndian, header); err != nil {
		t.Fatal(err)
	}
	if buf.Len() > replayHeaderSize {
		t.Fatalf("encoded header is too large: %d", buf.Len())
	}
	out := make([]byte, replayHeaderSize)
	copy(out, buf.Bytes())
	if cfg.version == replayVersionZstd {
		binary.LittleEndian.PutUint16(out[newSettingsSizeOffset:newSettingsSizeOffset+2], uint16(len(cfg.settings)))
		binary.LittleEndian.PutUint32(out[newResultsOffsetOffset:newResultsOffsetOffset+4], uint32(resultsOffset))
	}
	return out
}

func testPartConfig(part byte, last bool, compression string) testReplayConfig {
	packets := []testPacketSpec{{packetType: 8, timestamp: testTimestamp(uint32(part+1) * 100)}}
	if last {
		packets = append(packets, testPacketSpec{packetType: replayPacketEndMarker})
	} else {
		packets = append(packets, testPacketSpec{packetType: replayPacketNextSegment})
	}
	return testReplayConfig{
		part:         part,
		compression:  compression,
		packetStream: encodeTestPacketsProbe(packets),
	}
}

func encodeTestPacketsProbe(specs []testPacketSpec) []byte {
	out := struct{ bytes.Buffer }{}
	for _, spec := range specs {
		headerSize := uint32(2)
		if spec.timestamp != nil {
			headerSize = 6
		}
		_ = packet.WriteVariableLengthSize(&out, headerSize+uint32(len(spec.payload)))
		header := [2]byte{spec.packetType, 0}
		if spec.timestamp == nil {
			header[0] |= 0b00010000
		}
		out.Write(header[:])
		if spec.timestamp != nil {
			_ = binary.Write(&out, binary.LittleEndian, *spec.timestamp)
		}
		out.Write(spec.payload)
	}
	return out.Bytes()
}

func TestOpenReplayDirectorySortsByHeaderAndCombines(t *testing.T) {
	dir := t.TempDir()
	part0Packets := encodeTestPackets(t, []testPacketSpec{
		{packetType: 3, timestamp: testTimestamp(100), payload: []byte("chat")},
		{packetType: replayPacketNextSegment},
	})
	part1Packets := encodeTestPackets(t, []testPacketSpec{
		{packetType: 8},
		{packetType: replayPacketNextSegment, timestamp: testTimestamp(200)},
	})
	part2Packets := encodeTestPackets(t, []testPacketSpec{
		{packetType: 4},
		{packetType: replayPacketEndMarker},
	})

	part0 := writeTestReplayPart(t, dir, testReplayConfig{
		filename:     "zzz.WRPL",
		part:         0,
		settings:     []byte("settings-zero"),
		packetStream: part0Packets,
	})
	part1 := writeTestReplayPart(t, dir, testReplayConfig{
		filename:     "aaa.wrpl",
		part:         1,
		settings:     []byte("ignored-settings"),
		packetStream: part1Packets,
		results:      []byte("earlier-results"),
	})
	part2 := writeTestReplayPart(t, dir, testReplayConfig{
		filename:     "mmm.wrpl",
		part:         2,
		packetStream: part2Packets,
		results:      []byte("last-results"),
	})
	if err := os.WriteFile(filepath.Join(dir, "ignored.txt"), []byte("not a replay"), 0o600); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(dir, "nested")
	if err := os.Mkdir(nested, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "broken.wrpl"), []byte("ignored"), 0o600); err != nil {
		t.Fatal(err)
	}

	before := testFileDigest(t, part0, part1, part2)
	rpl, info, err := OpenReplayDirectory(dir)
	if err != nil {
		t.Fatal(err)
	}
	if before != testFileDigest(t, part0, part1, part2) {
		t.Fatal("OpenReplayDirectory modified input files")
	}
	if info.SessionID != testReplaySession {
		t.Fatalf("session = %016x", info.SessionID)
	}
	if rpl.Header.ReplayPartNumber != 0 || !rpl.Header.IsServer() {
		t.Fatalf("header must come from part 0: %#v", rpl.Header)
	}
	if got, want := fmt.Sprint(info.PartNumbers), fmt.Sprint([]byte{0, 1, 2}); got != want {
		t.Fatalf("part numbers = %s, want %s", got, want)
	}
	wantPaths := []string{part0, part1, part2}
	if fmt.Sprint(info.Paths) != fmt.Sprint(wantPaths) {
		t.Fatalf("paths = %v, want %v", info.Paths, wantPaths)
	}
	if string(rpl.Settings) != "settings-zero" {
		t.Fatalf("settings = %q", rpl.Settings)
	}
	if string(rpl.Results) != "last-results" {
		t.Fatalf("results = %q", rpl.Results)
	}

	reader := packet.NewPacketStreamReader(rpl.PacketStream)
	pk := &packet.Packet{}
	var types []byte
	var times []uint32
	for seq := uint64(0); ; seq++ {
		eof, err := reader.ReadPacket(pk)
		if err != nil {
			t.Fatalf("packet %d: %v", seq, err)
		}
		if eof {
			break
		}
		if pk.Seq != 0 {
			t.Fatalf("raw packet unexpectedly carried Seq %d", pk.Seq)
		}
		types = append(types, pk.PacketType)
		times = append(times, pk.CurrentTime)
	}
	if fmt.Sprint(types) != fmt.Sprint([]byte{3, 5, 8, 5, 4, 0}) {
		t.Fatalf("packet types = %v", types)
	}
	if fmt.Sprint(times) != fmt.Sprint([]uint32{100, 100, 100, 200, 200, 200}) {
		t.Fatalf("timestamps did not carry across parts: %v", times)
	}
	if err := rpl.Close(); err != nil {
		t.Fatal(err)
	}
	if err := rpl.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if _, err := rpl.PacketStream.Read(make([]byte, 1)); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("Read after Close = %v", err)
	}
}

func TestOpenReplayDirectoryAcceptsLegacyOddPartNumbers(t *testing.T) {
	dir := t.TempDir()
	for i, part := range []byte{0, 1, 3, 5} {
		cfg := testPartConfig(part, i == 3, "zlib")
		cfg.version = replayVersionZstd - 1
		cfg.filename = fmt.Sprintf("part-%d.wrpl", 5-i)
		writeTestReplayPart(t, dir, cfg)
	}
	rpl, info, err := OpenReplayDirectory(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer rpl.Close()
	if fmt.Sprint(info.PartNumbers) != fmt.Sprint([]byte{0, 1, 3, 5}) {
		t.Fatalf("part numbers = %v", info.PartNumbers)
	}
	data, err := io.ReadAll(rpl.PacketStream)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 {
		t.Fatal("empty combined packet stream")
	}
}

func TestOpenReplayDirectoryAllowsOptionalEvenSnapshots(t *testing.T) {
	dir := t.TempDir()
	for i, part := range []byte{0, 1, 3, 4, 5} {
		writeTestReplayPart(t, dir, testPartConfig(part, i == 4, "zstd"))
	}
	rpl, info, err := OpenReplayDirectory(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer rpl.Close()
	if fmt.Sprint(info.PartNumbers) != fmt.Sprint([]byte{0, 1, 3, 4, 5}) {
		t.Fatalf("part numbers = %v", info.PartNumbers)
	}
}

func TestOpenReplayDirectoryAllowIncompleteOnlyForMissingTail(t *testing.T) {
	dir := t.TempDir()
	writeTestReplayPart(t, dir, testPartConfig(0, false, "zstd"))
	writeTestReplayPart(t, dir, testPartConfig(1, false, "zstd"))

	if rpl, info, err := OpenReplayDirectory(dir); err == nil {
		_ = rpl.Close()
		t.Fatalf("default API accepted missing tail: %+v", info)
	}

	rpl, info, err := OpenReplayDirectoryWithOptions(dir, ReplayDirectoryOptions{AllowIncomplete: true})
	if err != nil {
		t.Fatal(err)
	}
	defer rpl.Close()
	if !info.Incomplete {
		t.Fatal("partial replay was marked complete")
	}
	if !strings.Contains(info.CompletenessNote, "part 1") ||
		!strings.Contains(info.CompletenessNote, "NextSegment") ||
		!strings.Contains(info.CompletenessNote, "tail") {
		t.Fatalf("completeness note = %q", info.CompletenessNote)
	}
	if got, want := fmt.Sprint(info.PartNumbers), fmt.Sprint([]byte{0, 1}); got != want {
		t.Fatalf("part numbers = %s, want %s", got, want)
	}
	if _, err := io.ReadAll(rpl.PacketStream); err != nil {
		t.Fatalf("partial packet stream is not readable: %v", err)
	}
}

func TestOpenReplayDirectoryAllowIncompleteKeepsCompleteStatus(t *testing.T) {
	dir := t.TempDir()
	writeTestReplayPart(t, dir, testPartConfig(0, false, "zstd"))
	writeTestReplayPart(t, dir, testPartConfig(1, true, "zstd"))
	rpl, info, err := OpenReplayDirectoryWithOptions(dir, ReplayDirectoryOptions{AllowIncomplete: true})
	if err != nil {
		t.Fatal(err)
	}
	defer rpl.Close()
	if info.Incomplete || info.CompletenessNote != "" {
		t.Fatalf("complete replay marked incomplete: %+v", info)
	}
}

func TestOpenReplayDirectoryAllowIncompleteAcceptsOtherMissingEndMarker(t *testing.T) {
	dir := t.TempDir()
	writeTestReplayPart(t, dir, testPartConfig(0, false, "zstd"))
	cfg := testPartConfig(1, true, "zstd")
	cfg.packetStream = encodeTestPacketsProbe([]testPacketSpec{{packetType: 8}})
	writeTestReplayPart(t, dir, cfg)
	rpl, info, err := OpenReplayDirectoryWithOptions(dir, ReplayDirectoryOptions{AllowIncomplete: true})
	if err != nil {
		t.Fatal(err)
	}
	defer rpl.Close()
	if !info.Incomplete || !strings.Contains(info.CompletenessNote, "packet type 8") {
		t.Fatalf("partial status = incomplete:%v note:%q", info.Incomplete, info.CompletenessNote)
	}
}

func TestOpenReplayDirectoryAllowIncompleteRejectsOtherDamage(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T, dir string)
	}{
		{
			name: "missing part zero",
			setup: func(t *testing.T, dir string) {
				writeTestReplayPart(t, dir, testPartConfig(1, false, "zstd"))
			},
		},
		{
			name: "missing main part",
			setup: func(t *testing.T, dir string) {
				for i, part := range []byte{0, 1, 3, 7} {
					writeTestReplayPart(t, dir, testPartConfig(part, i == 3, "zstd"))
				}
			},
		},
		{
			name: "mixed sessions",
			setup: func(t *testing.T, dir string) {
				writeTestReplayPart(t, dir, testPartConfig(0, false, "zstd"))
				cfg := testPartConfig(1, false, "zstd")
				cfg.session = testReplaySession + 1
				writeTestReplayPart(t, dir, cfg)
			},
		},
		{
			name: "mixed versions",
			setup: func(t *testing.T, dir string) {
				writeTestReplayPart(t, dir, testPartConfig(0, false, "zstd"))
				cfg := testPartConfig(1, false, "zstd")
				cfg.version = replayVersionZstd - 1
				writeTestReplayPart(t, dir, cfg)
			},
		},
		{
			name: "truncated compressed final part",
			setup: func(t *testing.T, dir string) {
				writeTestReplayPart(t, dir, testPartConfig(0, false, "zstd"))
				cfg := testPartConfig(1, true, "zstd")
				cfg.truncateBy = 2
				writeTestReplayPart(t, dir, cfg)
			},
		},
		{
			name: "invalid final packet framing",
			setup: func(t *testing.T, dir string) {
				writeTestReplayPart(t, dir, testPartConfig(0, false, "zstd"))
				cfg := testPartConfig(1, false, "zstd")
				cfg.packetStream = []byte{0x81, 0x10}
				writeTestReplayPart(t, dir, cfg)
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			tc.setup(t, dir)
			rpl, info, err := OpenReplayDirectoryWithOptions(dir, ReplayDirectoryOptions{AllowIncomplete: true})
			if err == nil {
				_ = rpl.Close()
				t.Fatalf("AllowIncomplete masked structural damage: %+v", info)
			}
			if rpl != nil || info != nil {
				t.Fatal("structural failure returned partial result")
			}
		})
	}
}

func TestOpenReplayDirectoryRejectsInvalidSets(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T, dir string)
	}{
		{
			name: "empty directory",
			setup: func(t *testing.T, dir string) {
			},
		},
		{
			name: "missing part zero",
			setup: func(t *testing.T, dir string) {
				cfg := testPartConfig(1, true, "zstd")
				writeTestReplayPart(t, dir, cfg)
			},
		},
		{
			name: "gap in current contiguous encoding",
			setup: func(t *testing.T, dir string) {
				writeTestReplayPart(t, dir, testPartConfig(0, false, "zstd"))
				writeTestReplayPart(t, dir, testPartConfig(2, true, "zstd"))
			},
		},
		{
			name: "gap in odd main parts with a snapshot",
			setup: func(t *testing.T, dir string) {
				for i, part := range []byte{0, 1, 4, 5} {
					writeTestReplayPart(t, dir, testPartConfig(part, i == 3, "zstd"))
				}
			},
		},
		{
			name: "snapshot skips beyond the next main part",
			setup: func(t *testing.T, dir string) {
				for i, part := range []byte{0, 1, 3, 5, 8} {
					writeTestReplayPart(t, dir, testPartConfig(part, i == 4, "zstd"))
				}
			},
		},
		{
			name: "duplicate part number",
			setup: func(t *testing.T, dir string) {
				cfg := testPartConfig(0, false, "zstd")
				cfg.filename = "a.wrpl"
				writeTestReplayPart(t, dir, cfg)
				cfg.filename = "b.wrpl"
				writeTestReplayPart(t, dir, cfg)
				writeTestReplayPart(t, dir, testPartConfig(1, true, "zstd"))
			},
		},
		{
			name: "mixed sessions",
			setup: func(t *testing.T, dir string) {
				writeTestReplayPart(t, dir, testPartConfig(0, false, "zstd"))
				cfg := testPartConfig(1, true, "zstd")
				cfg.session = testReplaySession + 1
				writeTestReplayPart(t, dir, cfg)
			},
		},
		{
			name: "mixed versions",
			setup: func(t *testing.T, dir string) {
				writeTestReplayPart(t, dir, testPartConfig(0, false, "zstd"))
				cfg := testPartConfig(1, true, "zstd")
				cfg.version = replayVersionZstd - 1
				writeTestReplayPart(t, dir, cfg)
			},
		},
		{
			name: "mixed levels",
			setup: func(t *testing.T, dir string) {
				writeTestReplayPart(t, dir, testPartConfig(0, false, "zstd"))
				cfg := testPartConfig(1, true, "zstd")
				cfg.level = "levels/another_level.bin"
				writeTestReplayPart(t, dir, cfg)
			},
		},
		{
			name: "client replay",
			setup: func(t *testing.T, dir string) {
				cfg := testPartConfig(0, true, "zstd")
				cfg.client = true
				writeTestReplayPart(t, dir, cfg)
			},
		},
		{
			name: "zero session",
			setup: func(t *testing.T, dir string) {
				cfg := testPartConfig(0, true, "zstd")
				cfg.session = 1
				path := writeTestReplayPart(t, dir, cfg)
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				binary.LittleEndian.PutUint64(data[732:740], 0)
				if err := os.WriteFile(path, data, 0o600); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "bad magic",
			setup: func(t *testing.T, dir string) {
				cfg := testPartConfig(0, true, "zstd")
				cfg.badMagic = true
				writeTestReplayPart(t, dir, cfg)
			},
		},
		{
			name: "non-last part lacks NextSegment",
			setup: func(t *testing.T, dir string) {
				cfg := testPartConfig(0, false, "zstd")
				cfg.packetStream = encodeTestPacketsProbe([]testPacketSpec{{packetType: 8}})
				writeTestReplayPart(t, dir, cfg)
				writeTestReplayPart(t, dir, testPartConfig(1, true, "zstd"))
			},
		},
		{
			name: "last part still has NextSegment",
			setup: func(t *testing.T, dir string) {
				writeTestReplayPart(t, dir, testPartConfig(0, false, "zstd"))
				writeTestReplayPart(t, dir, testPartConfig(1, false, "zstd"))
			},
		},
		{
			name: "last part lacks EndMarker",
			setup: func(t *testing.T, dir string) {
				writeTestReplayPart(t, dir, testPartConfig(0, false, "zstd"))
				cfg := testPartConfig(1, true, "zstd")
				cfg.packetStream = encodeTestPacketsProbe([]testPacketSpec{{packetType: 8}})
				writeTestReplayPart(t, dir, cfg)
			},
		},
		{
			name: "EndMarker before later parts",
			setup: func(t *testing.T, dir string) {
				cfg := testPartConfig(0, true, "zstd")
				writeTestReplayPart(t, dir, cfg)
				writeTestReplayPart(t, dir, testPartConfig(1, true, "zstd"))
			},
		},
		{
			name: "packet follows EndMarker in one part",
			setup: func(t *testing.T, dir string) {
				cfg := testPartConfig(0, true, "zstd")
				cfg.packetStream = encodeTestPacketsProbe([]testPacketSpec{
					{packetType: replayPacketEndMarker},
					{packetType: 8},
				})
				writeTestReplayPart(t, dir, cfg)
			},
		},
		{
			name: "empty decompressed packet stream",
			setup: func(t *testing.T, dir string) {
				cfg := testPartConfig(0, true, "zstd")
				cfg.packetStream = []byte{0x80}
				writeTestReplayPart(t, dir, cfg)
			},
		},
		{
			name: "partial variable length prefix",
			setup: func(t *testing.T, dir string) {
				cfg := testPartConfig(0, true, "zstd")
				cfg.packetStream = []byte{0x00}
				writeTestReplayPart(t, dir, cfg)
			},
		},
		{
			name: "packet size too small",
			setup: func(t *testing.T, dir string) {
				cfg := testPartConfig(0, true, "zstd")
				cfg.packetStream = []byte{0x81, 0x10}
				writeTestReplayPart(t, dir, cfg)
			},
		},
		{
			name: "timestamped packet header too small",
			setup: func(t *testing.T, dir string) {
				cfg := testPartConfig(0, true, "zstd")
				var raw bytes.Buffer
				_ = packet.WriteVariableLengthSize(&raw, 2)
				raw.Write([]byte{8, 0})
				cfg.packetStream = raw.Bytes()
				writeTestReplayPart(t, dir, cfg)
			},
		},
		{
			name: "packet payload exceeds limit",
			setup: func(t *testing.T, dir string) {
				cfg := testPartConfig(0, true, "zstd")
				var raw bytes.Buffer
				_ = packet.WriteVariableLengthSize(&raw, uint32(packet.MaxPacketPayloadSize+7))
				raw.Write([]byte{8 | 0x10, 0})
				cfg.packetStream = raw.Bytes()
				writeTestReplayPart(t, dir, cfg)
			},
		},
		{
			name: "wrpl entry is a directory",
			setup: func(t *testing.T, dir string) {
				if err := os.Mkdir(filepath.Join(dir, "directory.wrpl"), 0o700); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "more than hard part limit",
			setup: func(t *testing.T, dir string) {
				for i := 0; i < maxReplayDirectoryParts+1; i++ {
					cfg := testPartConfig(0, true, "zstd")
					cfg.filename = fmt.Sprintf("%03d.wrpl", i)
					writeTestReplayPart(t, dir, cfg)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			tc.setup(t, dir)
			rpl, info, err := OpenReplayDirectory(dir)
			if err == nil {
				_ = rpl.Close()
				t.Fatalf("expected error, got info %+v", info)
			}
			if rpl != nil || info != nil {
				t.Fatalf("failure returned a partial replay: rpl=%v info=%v", rpl, info)
			}
		})
	}
}

func TestOpenReplayDirectoryRejectsSymlinkEntry(t *testing.T) {
	dir := t.TempDir()
	valid := writeTestReplayPart(t, dir, testPartConfig(0, true, "zstd"))
	link := filepath.Join(dir, "linked.wrpl")
	if err := os.Symlink(valid, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if rpl, info, err := OpenReplayDirectory(dir); err == nil {
		_ = rpl.Close()
		t.Fatalf("expected symlink error, got %+v", info)
	}
}

func TestOpenReplayDirectoryRejectsTruncatedCompressedStreams(t *testing.T) {
	for _, compression := range []string{"zlib", "zstd"} {
		t.Run(compression, func(t *testing.T) {
			dir := t.TempDir()
			cfg := testPartConfig(0, true, compression)
			if compression != "zstd" {
				cfg.version = replayVersionZstd - 1
			}
			cfg.truncateBy = 2
			writeTestReplayPart(t, dir, cfg)
			if rpl, info, err := OpenReplayDirectory(dir); err == nil {
				_ = rpl.Close()
				t.Fatalf("truncated %s stream accepted: %+v", compression, info)
			}
		})
	}
}

func TestOpenReplayDirectoryBoundsResults(t *testing.T) {
	dir := t.TempDir()
	cfg := testPartConfig(0, true, "zstd")
	cfg.sparseResultsSize = maxReplayResultsSize + 1
	writeTestReplayPart(t, dir, cfg)
	if rpl, info, err := OpenReplayDirectory(dir); err == nil {
		_ = rpl.Close()
		t.Fatalf("oversized results accepted: %+v", info)
	}
}

func TestReplayDirectoryStreamClosesActivePart(t *testing.T) {
	dir := t.TempDir()
	writeTestReplayPart(t, dir, testPartConfig(0, false, "zstd"))
	writeTestReplayPart(t, dir, testPartConfig(1, true, "zstd"))
	rpl, _, err := OpenReplayDirectory(dir)
	if err != nil {
		t.Fatal(err)
	}
	stream, ok := rpl.PacketStream.(*replayDirectoryStream)
	if !ok {
		t.Fatalf("PacketStream = %T", rpl.PacketStream)
	}
	if _, err := stream.Read(make([]byte, 1)); err != nil {
		t.Fatal(err)
	}
	if stream.file == nil || stream.current == nil {
		t.Fatal("expected exactly one active part to be open")
	}
	if err := stream.Close(); err != nil {
		t.Fatal(err)
	}
	if stream.file != nil || stream.current != nil {
		t.Fatal("Close left an active file or decompressor")
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if _, err := stream.Read(make([]byte, 1)); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("Read after Close = %v", err)
	}
}

func TestPacketReaderRejectsUnsafeSizes(t *testing.T) {
	tests := []struct {
		name string
		raw  []byte
	}{
		{
			name: "negative timestamp payload",
			raw:  []byte{0x82, 8, 0},
		},
		{
			name: "huge payload",
			raw: func() []byte {
				var buf bytes.Buffer
				_ = packet.WriteVariableLengthSize(&buf, uint32(packet.MaxPacketPayloadSize+7))
				buf.Write([]byte{8 | 0x10, 0})
				return buf.Bytes()
			}(),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if recovered := recover(); recovered != nil {
					t.Fatalf("PacketReader panicked: %v", recovered)
				}
			}()
			reader := packet.NewPacketStreamReader(bytes.NewReader(tc.raw))
			pk := &packet.Packet{}
			if _, err := reader.ReadPacket(pk); err == nil {
				t.Fatal("expected invalid packet error")
			}
		})
	}
}

func TestReadVariableLengthSizeRejectsPartialPrefixes(t *testing.T) {
	for _, prefix := range [][]byte{{0x00}, {0x40}, {0x20}, {0x10}} {
		_, err := packet.ReadVariableLengthSize(bytes.NewReader(prefix))
		if !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Fatalf("prefix %x: error = %v, want ErrUnexpectedEOF", prefix, err)
		}
	}
}

func testFileDigest(t *testing.T, paths ...string) [32]byte {
	t.Helper()
	hash := sha256.New()
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = hash.Write(data)
	}
	var sum [32]byte
	copy(sum[:], hash.Sum(nil))
	return sum
}

func TestOpenReplayDirectoryErrorMentionsEmptyDirectoryFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "readme.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, err := OpenReplayDirectory(dir)
	if err == nil || !strings.Contains(err.Error(), ".wrpl") {
		t.Fatalf("error = %v", err)
	}
}
