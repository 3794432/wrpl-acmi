package terrainbundle

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var testData = []byte{0, 0, 1, 0, 2, 0, 3, 0}

func TestPackExtractRoundTripAndZIP64Footer(t *testing.T) {
	t.Parallel()
	source := makeTestSource(t, "berlin", testData)
	stubData := []byte("MZ-stub-with-prefix\x00payload")
	stub := writeTestFile(t, filepath.Join(t.TempDir(), "stub.exe"), stubData)
	out := filepath.Join(t.TempDir(), "terrain-installer.exe")

	if err := Pack(stub, source, out); err != nil {
		t.Fatalf("Pack: %v", err)
	}
	packed, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(packed, stubData) {
		t.Fatal("packed executable does not preserve the stub prefix")
	}
	offset := binary.LittleEndian.Uint64(packed[len(packed)-FooterSize+16 : len(packed)-FooterSize+24])
	size := binary.LittleEndian.Uint64(packed[len(packed)-FooterSize+24 : len(packed)-FooterSize+32])
	if got := string(packed[len(packed)-FooterSize : len(packed)-FooterSize+16]); got != PayloadMagic {
		t.Fatalf("magic = %q", got)
	}
	if int(offset) != len(stubData) {
		t.Fatalf("archive offset = %d, want %d", offset, len(stubData))
	}
	if int(offset+size)+FooterSize != len(packed) {
		t.Fatalf("footer bounds offset=%d size=%d file=%d", offset, size, len(packed))
	}
	archive := packed[offset : offset+size]
	if digest := sha256.Sum256(archive); !bytes.Equal(digest[:], packed[len(packed)-32:]) {
		t.Fatal("footer SHA-256 does not cover the ZIP payload")
	}
	if !bytes.Contains(archive, []byte{'P', 'K', 6, 6}) {
		t.Fatal("payload does not contain a ZIP64 end-of-central-directory record")
	}
	zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		t.Fatalf("open packed ZIP64 archive: %v", err)
	}
	if len(zr.File) != 5 {
		t.Fatalf("ZIP has %d files, want 5", len(zr.File))
	}
	for _, entry := range zr.File {
		if entry.Method != zip.Deflate {
			t.Fatalf("%s uses method %d, want Deflate", entry.Name, entry.Method)
		}
		if !strings.HasPrefix(entry.Name, "maps/") && entry.Name != "AtlasReferences.json" && entry.Name != "GeoReferences.json" {
			t.Fatalf("unexpected archive entry %q", entry.Name)
		}
	}

	destination := filepath.Join(t.TempDir(), "terrain")
	found, err := Extract(out, destination)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if !found {
		t.Fatal("Extract reported no payload")
	}
	assertTreeEqual(t, source, destination)
	if _, err := os.Stat(filepath.Join(destination, "manifest.json")); !os.IsNotExist(err) {
		t.Fatalf("manifest.json unexpectedly present: %v", err)
	}
}

func TestRealReferenceFormatsAndWrappedFixturePack(t *testing.T) {
	t.Parallel()
	repositoryRoot := filepath.Join("..", "..")

	flatPath := filepath.Join(repositoryRoot, "tools", "terrain", "AtlasReferences.json")
	flatIDs, flatDigest, err := loadAtlasReferences(flatPath)
	if err != nil {
		t.Fatalf("load published AtlasReferences.json: %v", err)
	}
	if len(flatIDs) != 71 || flatIDs[0] != "air_afghan" || flatIDs[len(flatIDs)-1] != "zhengzhou" {
		t.Fatalf("published AtlasReferences IDs = %d, first=%q last=%q", len(flatIDs), flatIDs[0], flatIDs[len(flatIDs)-1])
	}
	if flatDigest == [32]byte{} {
		t.Fatal("published AtlasReferences digest is empty")
	}
	if _, err := validateGeoReferences(filepath.Join(repositoryRoot, "tools", "terrain", "GeoReferences.json")); err != nil {
		t.Fatalf("load published GeoReferences.json: %v", err)
	}

	fixtureRoot := makeWrappedFixtureSource(t)
	wrappedIDs, wrappedDigest, err := loadAtlasReferences(filepath.Join(fixtureRoot, "AtlasReferences.json"))
	if err != nil {
		t.Fatalf("load fixture AtlasReferences.json: %v", err)
	}
	if fmt.Sprint(wrappedIDs) != "[geo_alpha geo_beta]" {
		t.Fatalf("fixture AtlasReferences IDs = %v", wrappedIDs)
	}
	if wrappedDigest == [32]byte{} {
		t.Fatal("fixture AtlasReferences digest is empty")
	}
	inputs, err := discoverInputs(fixtureRoot)
	if err != nil {
		t.Fatalf("discover wrapped fixture: %v", err)
	}
	if len(inputs) != 8 {
		t.Fatalf("discover wrapped fixture returned %d inputs, want 8", len(inputs))
	}

	stub := writeTestFile(t, filepath.Join(t.TempDir(), "fixture-stub.exe"), []byte("MZ-real-fixture-stub"))
	out := filepath.Join(t.TempDir(), "fixture-installer.exe")
	if err := Pack(stub, fixtureRoot, out); err != nil {
		t.Fatalf("Pack wrapped fixture: %v", err)
	}
	destination := filepath.Join(t.TempDir(), "fixture-output")
	found, err := Extract(out, destination)
	if err != nil || !found {
		t.Fatalf("Extract wrapped fixture: found=%v err=%v", found, err)
	}
	for _, relative := range []string{
		"AtlasReferences.json",
		"GeoReferences.json",
		"maps/geo_alpha/metadata.json",
		"maps/geo_alpha/geo_alpha.data",
		"maps/geo_alpha/CustomHeightmapList.xml",
		"maps/geo_beta/metadata.json",
		"maps/geo_beta/geo_beta.data",
		"maps/geo_beta/CustomHeightmapList.xml",
	} {
		want, err := os.ReadFile(filepath.Join(fixtureRoot, filepath.FromSlash(relative)))
		if err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(filepath.Join(destination, filepath.FromSlash(relative)))
		if err != nil {
			t.Fatalf("read extracted wrapped fixture file %s: %v", relative, err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("wrapped fixture file %s differs after round trip", relative)
		}
	}
}

func makeWrappedFixtureSource(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	testdata := filepath.Join("testdata", "geographic")
	for _, name := range []string{"AtlasReferences.json", "GeoReferences.json"} {
		data, err := os.ReadFile(filepath.Join(testdata, name))
		if err != nil {
			t.Fatalf("read tracked wrapped reference %s: %v", name, err)
		}
		writeTestFile(t, filepath.Join(root, name), data)
	}
	for index, id := range []string{"geo_alpha", "geo_beta"} {
		data := bytes.Repeat([]byte{byte(index), 0}, 16)
		folder := filepath.Join(root, "maps", id)
		metadata := fmt.Sprintf(`{"schema_version":1,"id":%q,"status":"ok","output":{"width":4,"height":4,"data_bytes":%d,"data_sha256":"%x"}}`, id, len(data), sha256.Sum256(data))
		writeTestFile(t, filepath.Join(folder, "metadata.json"), []byte(metadata))
		writeTestFile(t, filepath.Join(folder, id+".data"), data)
		writeTestFile(t, filepath.Join(folder, "CustomHeightmapList.xml"), []byte("<CustomHeightmapList id=\""+id+"\"/>"))
	}
	return root
}

func TestExtractWithoutPayloadAndCorruptFooterOrDigest(t *testing.T) {
	t.Parallel()
	stub := writeTestFile(t, filepath.Join(t.TempDir(), "plain.exe"), bytes.Repeat([]byte("stub"), 32))
	destination := filepath.Join(t.TempDir(), "plain")
	found, err := Extract(stub, destination)
	if err != nil || found {
		t.Fatalf("plain executable: found=%v err=%v", found, err)
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatalf("plain executable created destination: %v", err)
	}

	source := makeTestSource(t, "berlin", testData)
	packedPath := filepath.Join(t.TempDir(), "packed.exe")
	if err := Pack(stub, source, packedPath); err != nil {
		t.Fatal(err)
	}
	packed, err := os.ReadFile(packedPath)
	if err != nil {
		t.Fatal(err)
	}

	badFooter := append([]byte(nil), packed...)
	binary.LittleEndian.PutUint64(badFooter[len(badFooter)-FooterSize+24:], 0)
	badFooterPath := writeTestFile(t, filepath.Join(t.TempDir(), "bad-footer.exe"), badFooter)
	if found, err := Extract(badFooterPath, filepath.Join(t.TempDir(), "bad-footer")); !found || err == nil {
		t.Fatalf("bad footer: found=%v err=%v", found, err)
	}

	badDigest := append([]byte(nil), packed...)
	offset := binary.LittleEndian.Uint64(badDigest[len(badDigest)-FooterSize+16 : len(badDigest)-FooterSize+24])
	badDigest[offset] ^= 0x01
	badDigestPath := writeTestFile(t, filepath.Join(t.TempDir(), "bad-digest.exe"), badDigest)
	destination = filepath.Join(t.TempDir(), "bad-digest")
	if found, err := Extract(badDigestPath, destination); !found || err == nil || !strings.Contains(err.Error(), "SHA-256") {
		t.Fatalf("bad digest: found=%v err=%v", found, err)
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatalf("bad digest created destination: %v", err)
	}
}

func TestExtractRejectsUnsafeArchiveNames(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		build func(*zip.Writer) error
	}{
		{name: "parent traversal", build: zipEntry("../escape.txt", []byte("escape"), 0)},
		{name: "absolute slash", build: zipEntry("/absolute.txt", []byte("absolute"), 0)},
		{name: "drive absolute", build: zipEntry("C:/absolute.txt", []byte("absolute"), 0)},
		{name: "backslash", build: zipEntry(`maps\evil.txt`, []byte("escape"), 0)},
		{name: "colon", build: zipEntry("maps/evil:name.txt", []byte("escape"), 0)},
		{name: "casefold duplicate", build: func(zw *zip.Writer) error {
			if err := addRawZipFile(zw, "Maps/One.txt", []byte("one"), 0); err != nil {
				return err
			}
			return addRawZipFile(zw, "maps/one.txt", []byte("two"), 0)
		}},
		{name: "symlink", build: zipEntry("maps/link", []byte("target"), os.ModeSymlink|0o777)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			archive := buildZip(t, test.build)
			root := t.TempDir()
			exe := writeInstallers(t, filepath.Join(root, "unsafe.exe"), []byte("stub"), archive)
			destination := filepath.Join(root, "out")
			found, err := Extract(exe, destination)
			if !found || err == nil {
				t.Fatalf("found=%v err=%v", found, err)
			}
			if _, err := os.Stat(destination); !os.IsNotExist(err) {
				t.Fatalf("unsafe archive created destination: %v", err)
			}
			if _, err := os.Stat(filepath.Join(root, "escape.txt")); !os.IsNotExist(err) {
				t.Fatalf("unsafe archive escaped destination: %v", err)
			}
		})
	}
}

func TestExtractRejectsDeclaredSizeAndEntryLimits(t *testing.T) {
	t.Parallel()
	t.Run("entry count", func(t *testing.T) {
		archive := buildZip(t, func(zw *zip.Writer) error {
			for index := 0; index <= maxEntries; index++ {
				if err := addRawZipFile(zw, fmt.Sprintf("file-%03d", index), nil, 0); err != nil {
					return err
				}
			}
			return nil
		})
		assertExtractRejected(t, archive, "maximum")
	})

	t.Run("single file", func(t *testing.T) {
		archive := buildZip(t, func(zw *zip.Writer) error {
			header := &zip.FileHeader{Name: "huge.bin", Method: zip.Store, UncompressedSize64: maxFileBytes + 1, CompressedSize64: 1}
			header.SetMode(0o644)
			writer, err := zw.CreateRaw(header)
			if err != nil {
				return err
			}
			_, err = writer.Write([]byte{0})
			return err
		})
		assertExtractRejected(t, archive, "exceeds")
	})

	t.Run("total size", func(t *testing.T) {
		archive := buildZip(t, func(zw *zip.Writer) error {
			for index := 0; index <= int(uint64(maxTotalBytes)/uint64(maxFileBytes)); index++ {
				header := &zip.FileHeader{
					Name:               fmt.Sprintf("part-%02d.bin", index),
					Method:             zip.Store,
					UncompressedSize64: maxFileBytes,
					CompressedSize64:   1,
				}
				header.SetMode(0o644)
				writer, err := zw.CreateRaw(header)
				if err != nil {
					return err
				}
				if _, err := writer.Write([]byte{0}); err != nil {
					return err
				}
			}
			return nil
		})
		assertExtractRejected(t, archive, "expands beyond")
	})
}

func TestExtractPropagatesZIPCRCError(t *testing.T) {
	t.Parallel()
	source := makeTestSource(t, "berlin", testData)
	root := t.TempDir()
	stub := writeTestFile(t, filepath.Join(root, "stub.exe"), bytes.Repeat([]byte("s"), 128))
	out := filepath.Join(root, "crc.exe")
	if err := Pack(stub, source, out); err != nil {
		t.Fatal(err)
	}
	packed, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	archiveOffset, archiveSize := installerArchiveBounds(t, packed)
	centralOffset, centralSize := archiveCentralBounds(t, packed[archiveOffset:archiveOffset+archiveSize])
	centralStart := archiveOffset + centralOffset
	if centralSize == 0 || binary.LittleEndian.Uint32(packed[centralStart:centralStart+4]) != 0x02014b50 {
		t.Fatal("central directory header not found")
	}
	packed[centralStart+16] ^= 0x01 // CRC-32 of the first entry.
	refreshFooterDigest(t, packed)
	corrupt := filepath.Join(root, "crc-corrupt.exe")
	if err := os.WriteFile(corrupt, packed, 0o600); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(root, "crc-output")
	found, err := Extract(corrupt, destination)
	if !found || err == nil {
		t.Fatalf("found=%v err=%v", found, err)
	}
	if !strings.Contains(strings.ToLower(err.Error()), "checksum") {
		t.Fatalf("CRC error was not identifiable: %v", err)
	}
	if _, statErr := os.Stat(destination); !os.IsNotExist(statErr) {
		t.Fatalf("CRC failure created destination: %v", statErr)
	}
}

func TestPackRejectsSourceCorruptionAndOverwrite(t *testing.T) {
	t.Parallel()
	t.Run("truncated data", func(t *testing.T) {
		source := makeTestSource(t, "berlin", testData)
		data := filepath.Join(source, "maps", "berlin", "berlin.data")
		if err := os.WriteFile(data, testData[:len(testData)-1], 0o600); err != nil {
			t.Fatal(err)
		}
		assertPackFails(t, source)
	})

	t.Run("data SHA-256 mismatch", func(t *testing.T) {
		source := makeTestSource(t, "berlin", testData)
		data := filepath.Join(source, "maps", "berlin", "berlin.data")
		changed := append([]byte(nil), testData...)
		changed[0] ^= 0x01
		if err := os.WriteFile(data, changed, 0o600); err != nil {
			t.Fatal(err)
		}
		assertPackFails(t, source)
	})

	t.Run("metadata dimensions type", func(t *testing.T) {
		source := makeTestSource(t, "berlin", testData)
		metadata := filepath.Join(source, "maps", "berlin", "metadata.json")
		bad := fmt.Sprintf(`{"id":"berlin","status":"ok","output":{"width":"2","height":2,"data_bytes":8,"data_sha256":"%x"}}`, sha256.Sum256(testData))
		if err := os.WriteFile(metadata, []byte(bad), 0o600); err != nil {
			t.Fatal(err)
		}
		assertPackFails(t, source)
	})

	t.Run("metadata byte count", func(t *testing.T) {
		source := makeTestSource(t, "berlin", testData)
		metadata := filepath.Join(source, "maps", "berlin", "metadata.json")
		bad := fmt.Sprintf(`{"id":"berlin","status":"ok","output":{"width":2,"height":2,"data_bytes":9,"data_sha256":"%x"}}`, sha256.Sum256(testData))
		if err := os.WriteFile(metadata, []byte(bad), 0o600); err != nil {
			t.Fatal(err)
		}
		assertPackFails(t, source)
	})

	t.Run("reference size limit", func(t *testing.T) {
		source := makeTestSource(t, "berlin", testData)
		if err := os.WriteFile(filepath.Join(source, "AtlasReferences.json"), bytes.Repeat([]byte(" "), maxReferenceBytes+1), 0o600); err != nil {
			t.Fatal(err)
		}
		assertPackFails(t, source)
	})

	t.Run("converter extras are ignored", func(t *testing.T) {
		source := makeTestSource(t, "berlin", testData)
		for _, name := range []string{"manifest.json", "catalog.json", "summary.json", "terrain-build.json", "overview.png"} {
			writeTestFile(t, filepath.Join(source, name), []byte("extra"))
		}
		writeTestFile(t, filepath.Join(source, "docs", "notes.txt"), []byte("not packed"))
		writeTestFile(t, filepath.Join(source, "tmp", "scratch.tmp"), []byte("not packed"))
		writeTestFile(t, filepath.Join(source, "maps", "berlin", "result.json"), []byte("{}"))
		writeTestFile(t, filepath.Join(source, "maps", "berlin", "cache", "ignored.bin"), []byte("not packed"))
		writeTestFile(t, filepath.Join(source, "maps", "README.txt"), []byte("not packed"))
		root := t.TempDir()
		stub := writeTestFile(t, filepath.Join(root, "stub.exe"), []byte("MZstub"))
		out := filepath.Join(root, "result.exe")
		if err := Pack(stub, source, out); err != nil {
			t.Fatalf("Pack rejected valid converter extras: %v", err)
		}
		packed, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		offset, size := installerArchiveBounds(t, packed)
		zr, err := zip.NewReader(bytes.NewReader(packed[offset:offset+size]), int64(size))
		if err != nil {
			t.Fatal(err)
		}
		if len(zr.File) != 5 {
			t.Fatalf("ZIP contains %d entries, want only 5 whitelist files", len(zr.File))
		}
		for _, entry := range zr.File {
			if strings.HasSuffix(entry.Name, "result.json") || strings.Contains(entry.Name, "cache/") || strings.Contains(entry.Name, "README") || strings.Contains(entry.Name, "docs/") || strings.Contains(entry.Name, "tmp/") {
				t.Fatalf("extra source file was packed: %q", entry.Name)
			}
		}
	})

	t.Run("existing target", func(t *testing.T) {
		source := makeTestSource(t, "berlin", testData)
		root := t.TempDir()
		stub := writeTestFile(t, filepath.Join(root, "stub.exe"), []byte("MZstub"))
		out := writeTestFile(t, filepath.Join(root, "existing.exe"), []byte("keep me"))
		if err := Pack(stub, source, out); err == nil {
			t.Fatal("Pack replaced an existing target")
		}
		data, err := os.ReadFile(out)
		if err != nil || string(data) != "keep me" {
			t.Fatalf("existing target changed: %q, %v", data, err)
		}
	})
}

func TestPackIgnoresExtraMapDirectories(t *testing.T) {
	t.Parallel()
	source := makeTestSource(t, "berlin", testData)
	extra := filepath.Join(source, "maps", "air_race_africa_canyon")
	writeTestFile(t, filepath.Join(extra, "metadata.json"), []byte("invalid and incomplete historical output"))
	writeTestFile(t, filepath.Join(extra, "partial.data"), []byte{0, 1, 2})
	writeTestFile(t, filepath.Join(extra, "nested", "ignored.bin"), []byte("not read"))

	root := t.TempDir()
	stub := writeTestFile(t, filepath.Join(root, "stub.exe"), []byte("MZstub"))
	out := filepath.Join(root, "result.exe")
	if err := Pack(stub, source, out); err != nil {
		t.Fatalf("Pack rejected an extra non-target map directory: %v", err)
	}
	packed, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	offset, size := installerArchiveBounds(t, packed)
	zr, err := zip.NewReader(bytes.NewReader(packed[offset:offset+size]), int64(size))
	if err != nil {
		t.Fatal(err)
	}
	if len(zr.File) != 5 {
		t.Fatalf("ZIP contains %d entries, want only AtlasReferences target files", len(zr.File))
	}
	for _, entry := range zr.File {
		if strings.Contains(entry.Name, "air_race_africa_canyon") {
			t.Fatalf("extra map directory was packed: %q", entry.Name)
		}
	}
}

func TestPackRejectsSymlinkData(t *testing.T) {
	t.Parallel()
	source := makeTestSource(t, "berlin", testData)
	data := filepath.Join(source, "maps", "berlin", "berlin.data")
	external := writeTestFile(t, filepath.Join(t.TempDir(), "outside.data"), testData)
	if err := os.Remove(data); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, data); err != nil {
		t.Skipf("creating symlink is unavailable: %v", err)
	}
	assertPackFails(t, source)
}

func TestExtractRefusesExistingDestination(t *testing.T) {
	t.Parallel()
	source := makeTestSource(t, "berlin", testData)
	root := t.TempDir()
	stub := writeTestFile(t, filepath.Join(root, "stub.exe"), []byte("MZstub"))
	out := filepath.Join(root, "installer.exe")
	if err := Pack(stub, source, out); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(root, "destination")
	if err := os.Mkdir(destination, 0o755); err != nil {
		t.Fatal(err)
	}
	keep := writeTestFile(t, filepath.Join(destination, "keep.txt"), []byte("keep"))
	found, err := Extract(out, destination)
	if !found || err == nil {
		t.Fatalf("found=%v err=%v", found, err)
	}
	data, readErr := os.ReadFile(keep)
	if readErr != nil || string(data) != "keep" {
		t.Fatalf("existing destination changed: %q, %v", data, readErr)
	}
}

func makeTestSource(t *testing.T, id string, data []byte) string {
	t.Helper()
	root := t.TempDir()
	atlas := fmt.Sprintf(`{"%s":{"latitude":1,"longitude":2}}`, id)
	geo := fmt.Sprintf(`{"schema":1,"source":{"repository":"test","commit":"test"},"maps":{"%s":{"enabled":false}}}`, id)
	writeTestFile(t, filepath.Join(root, "AtlasReferences.json"), []byte(atlas))
	writeTestFile(t, filepath.Join(root, "GeoReferences.json"), []byte(geo))
	folder := filepath.Join(root, "maps", id)
	metadata := fmt.Sprintf(`{"id":%q,"status":"ok","output":{"width":2,"height":2,"data_bytes":%d,"data_sha256":"%x","extra":true}}`, id, len(data), sha256.Sum256(data))
	writeTestFile(t, filepath.Join(folder, "metadata.json"), []byte(metadata))
	writeTestFile(t, filepath.Join(folder, id+".data"), data)
	writeTestFile(t, filepath.Join(folder, "CustomHeightmapList.xml"), []byte("<CustomHeightmapList id=\""+id+"\"/>"))
	return root
}

func assertPackFails(t *testing.T, source string) {
	t.Helper()
	root := t.TempDir()
	stub := writeTestFile(t, filepath.Join(root, "stub.exe"), []byte("MZstub"))
	out := filepath.Join(root, "result.exe")
	if err := Pack(stub, source, out); err == nil {
		t.Fatal("Pack accepted corrupt input")
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatalf("failed Pack left an output: %v", err)
	}
}

func writeTestFile(t *testing.T, name string, data []byte) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return name
}

func assertTreeEqual(t *testing.T, source, destination string) {
	t.Helper()
	want := []string{
		"AtlasReferences.json",
		"GeoReferences.json",
		"maps/berlin/metadata.json",
		"maps/berlin/berlin.data",
		"maps/berlin/CustomHeightmapList.xml",
	}
	for _, relative := range want {
		wantData, err := os.ReadFile(filepath.Join(source, filepath.FromSlash(relative)))
		if err != nil {
			t.Fatal(err)
		}
		gotData, err := os.ReadFile(filepath.Join(destination, filepath.FromSlash(relative)))
		if err != nil {
			t.Fatalf("read extracted %s: %v", relative, err)
		}
		if !bytes.Equal(gotData, wantData) {
			t.Fatalf("extracted %s differs", relative)
		}
	}
}

func zipEntry(name string, body []byte, mode os.FileMode) func(*zip.Writer) error {
	return func(zw *zip.Writer) error { return addRawZipFile(zw, name, body, mode) }
}

func addRawZipFile(zw *zip.Writer, name string, body []byte, mode os.FileMode) error {
	header := &zip.FileHeader{Name: name, Method: zip.Store}
	if mode != 0 {
		header.SetMode(mode)
	} else {
		header.SetMode(0o644)
	}
	writer, err := zw.CreateHeader(header)
	if err != nil {
		return err
	}
	_, err = writer.Write(body)
	return err
}

func buildZip(t *testing.T, add func(*zip.Writer) error) []byte {
	t.Helper()
	var buffer bytes.Buffer
	zw := zip.NewWriter(&buffer)
	if err := add(zw); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func writeInstallers(t *testing.T, name string, stub, archive []byte) string {
	t.Helper()
	payload := append([]byte(nil), stub...)
	offset := len(payload)
	payload = append(payload, archive...)
	footer := make([]byte, FooterSize)
	copy(footer, PayloadMagic)
	binary.LittleEndian.PutUint64(footer[16:24], uint64(offset))
	binary.LittleEndian.PutUint64(footer[24:32], uint64(len(archive)))
	digest := sha256.Sum256(archive)
	copy(footer[32:], digest[:])
	payload = append(payload, footer...)
	return writeTestFile(t, name, payload)
}

func assertExtractRejected(t *testing.T, archive []byte, want string) {
	t.Helper()
	root := t.TempDir()
	exe := writeInstallers(t, filepath.Join(root, "limits.exe"), []byte("stub"), archive)
	destination := filepath.Join(root, "out")
	found, err := Extract(exe, destination)
	if !found || err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("found=%v err=%v, want substring %q", found, err, want)
	}
	if _, statErr := os.Stat(destination); !os.IsNotExist(statErr) {
		t.Fatalf("limit failure created destination: %v", statErr)
	}
}

func installerArchiveBounds(t *testing.T, payload []byte) (int, int) {
	t.Helper()
	if len(payload) < FooterSize {
		t.Fatal("payload too short")
	}
	offset := int(binary.LittleEndian.Uint64(payload[len(payload)-FooterSize+16 : len(payload)-FooterSize+24]))
	size := int(binary.LittleEndian.Uint64(payload[len(payload)-FooterSize+24 : len(payload)-FooterSize+32]))
	return offset, size
}

func archiveCentralBounds(t *testing.T, archive []byte) (int, int) {
	t.Helper()
	if len(archive) < 22 || binary.LittleEndian.Uint32(archive[len(archive)-22:]) != zipEndSignature {
		t.Fatal("ZIP end record missing")
	}
	end := archive[len(archive)-22:]
	centralOffset := binary.LittleEndian.Uint32(end[16:20])
	centralSize := binary.LittleEndian.Uint32(end[12:16])
	if centralOffset == 0xffffffff && len(archive) >= 98 {
		zip64 := archive[len(archive)-98:]
		if binary.LittleEndian.Uint32(zip64[:4]) != zip64EndSignature {
			t.Fatal("ZIP64 end record missing")
		}
		centralSize = uint32(binary.LittleEndian.Uint64(zip64[40:48]))
		centralOffset = uint32(binary.LittleEndian.Uint64(zip64[48:56]))
	}
	return int(centralOffset), int(centralSize)
}

func refreshFooterDigest(t *testing.T, payload []byte) {
	t.Helper()
	offset, size := installerArchiveBounds(t, payload)
	digest := sha256.Sum256(payload[offset : offset+size])
	copy(payload[len(payload)-32:], digest[:])
}
