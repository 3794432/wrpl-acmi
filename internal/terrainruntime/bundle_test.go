package terrainruntime

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maxsupermanhd/wrpl-inspector/v3/internal/terrainbundle"
)

func writeFile(t *testing.T, name string, data []byte) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return name
}

func fixtureFiles() map[string][]byte {
	return map[string][]byte{
		"python.exe":                            []byte("MZ-fake-python-3.12.10"),
		"python312.dll":                         []byte("fake DLL"),
		"Lib/site-packages/example/__init__.py": []byte("# synthetic runtime fixture\n"),
		"licenses/python.txt":                   []byte("fixture license"),
		"empty.txt":                             {},
	}
}

func manifestFor(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	m := Manifest{Schema: 1, PythonVersion: "3.12.10", Files: make(map[string]File)}
	for name, data := range files {
		m.Files[name] = metadata(data)
	}
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func fixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := fixtureFiles()
	for name, data := range files {
		writeFile(t, filepath.Join(root, filepath.FromSlash(name)), data)
	}
	writeFile(t, filepath.Join(root, ManifestName), manifestFor(t, files))
	return root
}

func packFixture(t *testing.T) (string, string) {
	t.Helper()
	root := fixture(t)
	stub := writeFile(t, filepath.Join(t.TempDir(), "stub.exe"), []byte("MZ synthetic stub\x00"))
	out := filepath.Join(t.TempDir(), "runtime.exe")
	if err := Pack(stub, root, out); err != nil {
		t.Fatal(err)
	}
	return out, root
}

func TestRoundTripAndSeparateKind(t *testing.T) {
	t.Parallel()
	out, root := packFixture(t)
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(data, []byte("MZ synthetic stub\x00")) {
		t.Fatal("stub changed")
	}
	footer := data[len(data)-FooterSize:]
	if len(PayloadMagic) != 16 || string(footer[:16]) != PayloadMagic {
		t.Fatal("bad runtime magic")
	}
	offset := binary.LittleEndian.Uint64(footer[16:24])
	size := binary.LittleEndian.Uint64(footer[24:32])
	if offset+size != uint64(len(data)-FooterSize) {
		t.Fatal("invalid archive bounds")
	}
	id, err := PayloadID(out)
	if err != nil || id != fmt.Sprintf("%x", sha256.Sum256(data[offset:offset+size])) {
		t.Fatalf("PayloadID = %q, %v", id, err)
	}
	dest := filepath.Join(t.TempDir(), "runtime")
	if found, err := Extract(out, dest); err != nil || !found {
		t.Fatalf("Extract = %v, %v", found, err)
	}
	if err := Validate(dest); err != nil {
		t.Fatal(err)
	}
	if err := Validate(root); err != nil {
		t.Fatal(err)
	}
	for name, want := range fixtureFiles() {
		got, err := os.ReadFile(filepath.Join(dest, filepath.FromSlash(name)))
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("%s = %q, %v", name, got, err)
		}
	}
	if found, err := terrainbundle.Extract(out, filepath.Join(t.TempDir(), "terrain")); found || err != nil {
		t.Fatalf("terrain loader accepted Python: %v, %v", found, err)
	}
	copy(footer[:16], terrainbundle.PayloadMagic)
	terrain := writeFile(t, filepath.Join(t.TempDir(), "terrain.exe"), data)
	if found, err := Extract(terrain, filepath.Join(t.TempDir(), "python")); found || err != nil {
		t.Fatalf("Python loader accepted terrain: %v, %v", found, err)
	}
	if _, err := PayloadID(terrain); !errors.Is(err, ErrNoPayload) {
		t.Fatalf("missing ID error = %v", err)
	}
}

func TestMissingAndCorruptPayload(t *testing.T) {
	t.Parallel()
	for _, data := range [][]byte{[]byte("stub"), bytes.Repeat([]byte("MZ"), 100)} {
		exe := writeFile(t, filepath.Join(t.TempDir(), "plain.exe"), data)
		dest := filepath.Join(t.TempDir(), "absent")
		if found, err := Extract(exe, dest); found || err != nil {
			t.Fatalf("plain Extract = %v, %v", found, err)
		}
		if _, err := PayloadID(exe); !errors.Is(err, ErrNoPayload) {
			t.Fatal(err)
		}
		assertAbsent(t, dest)
	}
	out, _ := packFixture(t)
	original, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		mutate func([]byte)
	}{
		{"offset overflow", func(b []byte) { binary.LittleEndian.PutUint64(b[len(b)-48:len(b)-40], ^uint64(0)) }},
		{"size overflow", func(b []byte) { binary.LittleEndian.PutUint64(b[len(b)-40:len(b)-32], ^uint64(0)) }},
		{"empty archive", func(b []byte) { binary.LittleEndian.PutUint64(b[len(b)-40:len(b)-32], 0) }},
		{"digest mismatch", func(b []byte) { b[len(b)-1] ^= 1 }},
		{"archive tamper", func(b []byte) { b[30] ^= 1 }},
	} {
		t.Run(test.name, func(t *testing.T) {
			b := bytes.Clone(original)
			test.mutate(b)
			exe := writeFile(t, filepath.Join(t.TempDir(), "bad.exe"), b)
			dest := filepath.Join(t.TempDir(), "absent")
			if found, err := Extract(exe, dest); !found || err == nil {
				t.Fatalf("bad Extract = %v, %v", found, err)
			}
			if _, err := PayloadID(exe); err == nil {
				t.Fatal("bad PayloadID succeeded")
			}
			assertAbsent(t, dest)
		})
	}
}

func assertAbsent(t *testing.T, name string) {
	t.Helper()
	if _, err := os.Lstat(name); !os.IsNotExist(err) {
		t.Fatalf("%s exists: %v", name, err)
	}
}

func TestNeverReplaceDestinationOrInputs(t *testing.T) {
	t.Parallel()
	out, source := packFixture(t)
	for _, kind := range []string{"empty directory", "populated directory", "regular file"} {
		t.Run(kind, func(t *testing.T) {
			dest := filepath.Join(t.TempDir(), "existing")
			if kind == "regular file" {
				writeFile(t, dest, []byte("preserve"))
			} else {
				if err := os.Mkdir(dest, 0o755); err != nil {
					t.Fatal(err)
				}
				if kind == "populated directory" {
					writeFile(t, filepath.Join(dest, "keep"), []byte("preserve"))
				}
			}
			before, err := os.Lstat(dest)
			if err != nil {
				t.Fatal(err)
			}
			if found, err := Extract(out, dest); !found || err == nil {
				t.Fatalf("Extract = %v, %v", found, err)
			}
			after, err := os.Lstat(dest)
			if err != nil || !os.SameFile(before, after) {
				t.Fatalf("destination replaced: %v", err)
			}
			keep := dest
			if kind == "populated directory" {
				keep = filepath.Join(dest, "keep")
			}
			if kind != "empty directory" {
				data, err := os.ReadFile(keep)
				if err != nil || string(data) != "preserve" {
					t.Fatal("existing data changed")
				}
			}
		})
	}
	stub := writeFile(t, filepath.Join(t.TempDir(), "stub.exe"), []byte("MZ keep stub"))
	for _, target := range []string{stub, out, filepath.Join(source, "new.exe")} {
		if err := Pack(stub, source, target); err == nil {
			t.Fatalf("Pack accepted %s", target)
		}
	}
	if data, err := os.ReadFile(stub); err != nil || string(data) != "MZ keep stub" {
		t.Fatal("stub was overwritten")
	}
	if err := Validate(source); err != nil {
		t.Fatal(err)
	}
	// Exercise the final atomic publication primitive directly, including a
	// destination created after preflight. An Lstat+Rename sequence is unsafe.
	for _, dir := range []bool{false, true} {
		parent := t.TempDir()
		from := filepath.Join(parent, "from")
		to := filepath.Join(parent, "to")
		if dir {
			if err := os.Mkdir(from, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(to, 0o755); err != nil {
				t.Fatal(err)
			}
		} else {
			writeFile(t, from, []byte("source"))
			writeFile(t, to, []byte("target"))
		}
		before, _ := os.Lstat(to)
		if err := publish(from, to); err == nil {
			t.Fatal("publication replaced existing destination")
		}
		after, err := os.Lstat(to)
		if err != nil || !os.SameFile(before, after) {
			t.Fatal("destination replaced")
		}
	}
}

func TestValidateAndPackRejectChangedTrees(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		change func(*testing.T, string)
	}{
		{"same size executable tamper", func(t *testing.T, r string) {
			writeFile(t, filepath.Join(r, "python.exe"), bytes.Repeat([]byte("x"), len(fixtureFiles()["python.exe"])))
		}},
		{"size mismatch", func(t *testing.T, r string) { writeFile(t, filepath.Join(r, "python.exe"), []byte("changed")) }},
		{"missing file", func(t *testing.T, r string) {
			if err := os.Remove(filepath.Join(r, "python312.dll")); err != nil {
				t.Fatal(err)
			}
		}},
		{"extra cache file", func(t *testing.T, r string) {
			writeFile(t, filepath.Join(r, "Lib", "__pycache__", "extra.pyc"), []byte("cache"))
		}},
		{"extra empty directory", func(t *testing.T, r string) {
			if err := os.Mkdir(filepath.Join(r, "extra"), 0o755); err != nil {
				t.Fatal(err)
			}
		}},
		{"incorrect case", func(t *testing.T, r string) {
			if err := os.Rename(filepath.Join(r, "python.exe"), filepath.Join(r, "temporary")); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(filepath.Join(r, "temporary"), filepath.Join(r, "Python.exe")); err != nil {
				t.Fatal(err)
			}
		}},
		{"manifest missing", func(t *testing.T, r string) {
			if err := os.Remove(filepath.Join(r, ManifestName)); err != nil {
				t.Fatal(err)
			}
		}},
		{"manifest oversized", func(t *testing.T, r string) {
			writeFile(t, filepath.Join(r, ManifestName), bytes.Repeat([]byte(" "), maxManifestBytes+1))
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			r := fixture(t)
			test.change(t, r)
			if err := Validate(r); err == nil {
				t.Fatal("Validate succeeded")
			}
			stub := writeFile(t, filepath.Join(t.TempDir(), "stub.exe"), []byte("MZ"))
			out := filepath.Join(t.TempDir(), "out.exe")
			if err := Pack(stub, r, out); err == nil {
				t.Fatal("Pack succeeded")
			}
			assertAbsent(t, out)
		})
	}
}

type zipEntry struct {
	name string
	data []byte
	mode os.FileMode
}

func archive(t *testing.T, entries []zipEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		h := &zip.FileHeader{Name: e.name, Method: zip.Store}
		mode := e.mode
		if mode == 0 {
			mode = 0o644
		}
		h.SetMode(mode)
		w, err := zw.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		data := e.data
		if strings.HasSuffix(e.name, "/") {
			data = nil
		}
		if _, err := w.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func wrapArchive(t *testing.T, data []byte) string {
	t.Helper()
	stub := []byte("MZ-runtime-test-stub")
	var footer [FooterSize]byte
	copy(footer[:], PayloadMagic)
	binary.LittleEndian.PutUint64(footer[16:24], uint64(len(stub)))
	binary.LittleEndian.PutUint64(footer[24:32], uint64(len(data)))
	h := sha256.Sum256(data)
	copy(footer[32:], h[:])
	all := append(bytes.Clone(stub), data...)
	all = append(all, footer[:]...)
	return writeFile(t, filepath.Join(t.TempDir(), "crafted.exe"), all)
}

func validEntries(t *testing.T) []zipEntry {
	t.Helper()
	files := fixtureFiles()
	result := []zipEntry{{ManifestName, manifestFor(t, files), 0}}
	for name, data := range files {
		result = append(result, zipEntry{name, data, 0})
	}
	return result
}

func rejectsArchive(t *testing.T, data []byte) error {
	t.Helper()
	exe := wrapArchive(t, data)
	parent := t.TempDir()
	dest := filepath.Join(parent, "runtime")
	found, err := Extract(exe, dest)
	if !found || err == nil {
		t.Fatalf("Extract = %v, %v", found, err)
	}
	assertAbsent(t, dest)
	remaining, readErr := os.ReadDir(parent)
	if readErr != nil || len(remaining) != 0 {
		t.Fatalf("temporary extraction leaked: %v %v", remaining, readErr)
	}
	return err
}

func TestRejectUnsafeArchivePathsAndTypes(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"../escape", "a/../../escape", "/absolute", "C:/absolute", `a\escape`, "python.exe:stream", "bad./x", "NUL.txt", "COM¹.txt", "a//b", "a/./b", "trailing/", "maps/height.txt", "Lib/maps/x.txt", "terrain.DATA", "x.BIN", "x.blk", "x.BLKX"} {
		t.Run(name, func(t *testing.T) {
			entries := validEntries(t)
			entries = append(entries, zipEntry{name, []byte("unsafe"), 0})
			rejectsArchive(t, archive(t, entries))
		})
	}
	for _, pair := range [][2]string{{"python.exe", "Python.exe"}, {"Lib/a.txt", "lib/b.txt"}, {"a", "a/b"}, {"a/b", "a"}, {"K/a", "K/b"}, {"same", "same"}} {
		t.Run(strings.Join(pair[:], "+"), func(t *testing.T) { rejectsArchive(t, archive(t, []zipEntry{{pair[0], nil, 0}, {pair[1], nil, 0}})) })
	}
	for _, mode := range []os.FileMode{os.ModeSymlink | 0o777, os.ModeNamedPipe | 0o600, os.ModeDir | 0o755} {
		t.Run(mode.String(), func(t *testing.T) { rejectsArchive(t, archive(t, []zipEntry{{"python.exe", []byte("target"), mode}})) })
	}
}

func TestRejectArchiveManifestAndContentMismatch(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"no manifest", "extra file", "missing file", "wrong case", "hash mismatch", "size mismatch", "CRC mismatch", "invalid manifest"} {
		t.Run(kind, func(t *testing.T) {
			entries := validEntries(t)
			switch kind {
			case "no manifest":
				entries = entries[1:]
			case "extra file":
				entries = append(entries, zipEntry{"extra.txt", []byte("extra"), 0})
			case "missing file":
				entries = entries[:len(entries)-1]
			case "invalid manifest":
				entries[0].data = []byte(`{"schema":1}`)
			default:
				for i := range entries {
					if entries[i].name == "python.exe" {
						switch kind {
						case "wrong case":
							entries[i].name = "Python.exe"
						case "hash mismatch":
							entries[i].data = bytes.Repeat([]byte("!"), len(entries[i].data))
						case "size mismatch":
							entries[i].data = []byte("x")
						}
					}
				}
			}
			data := archive(t, entries)
			if kind == "CRC mismatch" {
				index := bytes.Index(data, fixtureFiles()["python.exe"])
				if index < 0 {
					t.Fatal("fixture not stored")
				}
				data[index] ^= 1
			}
			err := rejectsArchive(t, data)
			if kind == "CRC mismatch" && !errors.Is(err, zip.ErrChecksum) {
				t.Fatalf("expected CRC error: %v", err)
			}
			if kind == "hash mismatch" && !strings.Contains(err.Error(), "SHA-256") {
				t.Fatalf("expected file hash error: %v", err)
			}
		})
	}
}

func TestZIPDirectoryAndExpansionLimits(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"not ZIP", "understated count", "oversized count", "oversized file", "oversized manifest", "total limit", "reparse attribute"} {
		t.Run(kind, func(t *testing.T) {
			data := archive(t, validEntries(t))
			end := data[len(data)-22:]
			central := int(binary.LittleEndian.Uint32(end[16:20]))
			switch kind {
			case "not ZIP":
				data = []byte("invalid archive")
			case "understated count":
				binary.LittleEndian.PutUint16(end[8:10], 1)
				binary.LittleEndian.PutUint16(end[10:12], 1)
			case "oversized count":
				binary.LittleEndian.PutUint16(end[8:10], maxFiles+1)
				binary.LittleEndian.PutUint16(end[10:12], maxFiles+1)
			case "oversized file":
				binary.LittleEndian.PutUint32(data[central+24:central+28], maxFileBytes+1)
			case "oversized manifest":
				binary.LittleEndian.PutUint32(data[central+24:central+28], maxManifestBytes+1)
			case "reparse attribute":
				binary.LittleEndian.PutUint32(data[central+38:central+42], 0x400)
			case "total limit":
				for pos := central; pos < len(data)-22; {
					binary.LittleEndian.PutUint32(data[pos+24:pos+28], maxFileBytes)
					pos += 46 + int(binary.LittleEndian.Uint16(data[pos+28:pos+30])) + int(binary.LittleEndian.Uint16(data[pos+30:pos+32])) + int(binary.LittleEndian.Uint16(data[pos+32:pos+34]))
				}
			}
			rejectsArchive(t, data)
		})
	}
	// More records than advertised must be caught before zip.NewReader, even
	// when the declared count appears to satisfy the bound.
	entries := make([]zipEntry, maxFiles+1)
	for i := range entries {
		entries[i] = zipEntry{fmt.Sprintf("f%d", i), nil, 0}
	}
	data := archive(t, entries)
	end := data[len(data)-22:]
	binary.LittleEndian.PutUint16(end[8:10], 1)
	binary.LittleEndian.PutUint16(end[10:12], 1)
	rejectsArchive(t, data)
}

// Opt-in verification of a builder-produced runtime, without executing Python
// or changing the source tree. Useful to the parent installer/build workflow.
func TestBuiltRuntimeRoundTrip(t *testing.T) {
	source := os.Getenv("WRPL_TEST_RUNTIME")
	if source == "" {
		t.Skip("set WRPL_TEST_RUNTIME to verify an assembled runtime")
	}
	if err := Validate(source); err != nil {
		t.Fatalf("builder runtime: %v", err)
	}
	stub := writeFile(t, filepath.Join(t.TempDir(), "stub.exe"), []byte("MZ synthetic stub for real runtime"))
	out := filepath.Join(t.TempDir(), "runtime.exe")
	if err := Pack(stub, source, out); err != nil {
		t.Fatal(err)
	}
	id, err := PayloadID(out)
	if err != nil {
		t.Fatal(err)
	}
	p, _, err := readPayload(out)
	if err != nil {
		t.Fatal(err)
	}
	defer p.file.Close()
	zr, err := zip.NewReader(io.NewSectionReader(p.file, p.offset, p.size), p.size)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range zr.File {
		if err := safeName(entry.Name); err != nil {
			t.Fatalf("game/unsafe file in real runtime: %v", err)
		}
	}
	dest := filepath.Join(t.TempDir(), "runtime")
	if found, err := Extract(out, dest); !found || err != nil {
		t.Fatalf("Extract = %v, %v", found, err)
	}
	if err := Validate(dest); err != nil {
		t.Fatal(err)
	}
	if err := ValidateEmbedded(out, dest); err != nil {
		t.Fatalf("real runtime cache pinning: %v", err)
	}
	t.Logf("verified %d ZIP members, archive=%d bytes, payload=%s; no game paths", len(zr.File), p.size, id)
}
