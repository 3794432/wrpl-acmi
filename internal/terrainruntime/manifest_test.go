package terrainruntime

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStrictManifestJSON(t *testing.T) {
	t.Parallel()
	hash := metadata(nil).SHA256
	file := fmt.Sprintf(`{"size":0,"sha256":%q}`, hash)
	valid := fmt.Sprintf(`{"schema":1,"python_version":"3.12.10","files":{"python.exe":%s}}`, file)
	if _, err := parseManifest([]byte(valid)); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ name, data string }{
		{"duplicate root key", strings.Replace(valid, `"schema":1`, `"schema":1,"schema":1`, 1)},
		{"duplicate file key", strings.Replace(valid, `"python.exe":`+file, `"python.exe":`+file+`,"python.exe":`+file, 1)},
		{"duplicate size", strings.Replace(valid, `"size":0`, `"size":0,"size":0`, 1)},
		{"root key case", strings.Replace(valid, `"schema"`, `"Schema"`, 1)},
		{"file key case", strings.Replace(valid, `"sha256"`, `"SHA256"`, 1)},
		{"unknown root field", strings.Replace(valid, `"schema":1`, `"schema":1,"extra":1`, 1)},
		{"unknown file field", strings.Replace(valid, `"size":0`, `"size":0,"extra":1`, 1)},
		{"missing size", strings.Replace(valid, `"size":0,`, "", 1)},
		{"missing hash", strings.Replace(valid, `,"sha256":"`+hash+`"`, "", 1)},
		{"missing version", strings.Replace(valid, `"python_version":"3.12.10",`, "", 1)},
		{"null schema", strings.Replace(valid, `"schema":1`, `"schema":null`, 1)},
		{"null size", strings.Replace(valid, `"size":0`, `"size":null`, 1)},
		{"null version", strings.Replace(valid, `"3.12.10"`, `null`, 1)},
		{"null files", `{"schema":1,"python_version":"3.12.10","files":null}`},
		{"null file", `{"schema":1,"python_version":"3.12.10","files":{"python.exe":null}}`},
		{"wrong schema", strings.Replace(valid, `"schema":1`, `"schema":2`, 1)},
		{"fractional size", strings.Replace(valid, `"size":0`, `"size":0.1`, 1)},
		{"negative size", strings.Replace(valid, `"size":0`, `"size":-1`, 1)},
		{"overflow size", strings.Replace(valid, `"size":0`, `"size":18446744073709551615`, 1)},
		{"nonhex hash", strings.Replace(valid, hash, strings.Repeat("g", 64), 1)},
		{"uppercase hash", strings.Replace(valid, hash, strings.ToUpper(hash), 1)},
		{"short hash", strings.Replace(valid, hash, "00", 1)},
		{"wrong python case", strings.Replace(valid, "python.exe", "Python.exe", 1)},
		{"trailing JSON", valid + `{}`},
		{"trailing junk", valid + `x`},
		{"nonobject", `[]`},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := parseManifest([]byte(test.data)); err == nil {
				t.Fatal("invalid manifest accepted")
			}
			rejectsArchive(t, archive(t, []zipEntry{{ManifestName, []byte(test.data), 0}, {"python.exe", nil, 0}}))
		})
	}
	if _, err := parseManifest(append([]byte(valid), 0xff)); err == nil {
		t.Fatal("invalid UTF-8 accepted")
	}
}

func TestManifestNamesAndLimits(t *testing.T) {
	t.Parallel()
	for _, names := range [][]string{
		{ManifestName}, {"RUNTIME-MANIFEST.JSON"}, {"../python.dll"}, {"/python.dll"}, {`C:\python.dll`},
		{"Lib/a", "lib/b"}, {"same", "same/child"}, {"maps/x.txt"}, {"x.data"}, {"x.bin"}, {"x.blk"}, {"x.blkx"},
	} {
		t.Run(strings.Join(names, "+"), func(t *testing.T) {
			files := map[string][]byte{"python.exe": nil}
			for _, name := range names {
				files[name] = nil
			}
			if _, err := parseManifest(manifestFor(t, files)); err == nil {
				t.Fatal("unsafe manifest accepted")
			}
		})
	}
	for _, kind := range []string{"file size", "total size", "file count"} {
		t.Run(kind, func(t *testing.T) {
			m := Manifest{1, "3.12.10", map[string]File{"python.exe": metadata(nil)}}
			switch kind {
			case "file size":
				m.Files["python.exe"] = File{maxFileBytes + 1, metadata(nil).SHA256}
			case "total size":
				for i := range 5 {
					m.Files[fmt.Sprintf("f%d", i)] = File{maxFileBytes, metadata(nil).SHA256}
				}
			case "file count":
				for i := range maxFiles {
					m.Files[fmt.Sprintf("f%d", i)] = metadata(nil)
				}
			}
			data, err := json.Marshal(m)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := parseManifest(data); err == nil {
				t.Fatal("manifest limit ignored")
			}
		})
	}
	if _, err := parseManifest(bytes.Repeat([]byte(" "), maxManifestBytes+1)); err == nil {
		t.Fatal("manifest byte limit ignored")
	}
}

func TestPackRejectsManifestedGameFiles(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"height.data", "height.BIN", "level.blk", "level.blkx", "maps/height.txt"} {
		t.Run(name, func(t *testing.T) {
			r := fixture(t)
			files := fixtureFiles()
			files[name] = []byte("game")
			writeFile(t, filepath.Join(r, filepath.FromSlash(name)), files[name])
			writeFile(t, filepath.Join(r, ManifestName), manifestFor(t, files))
			if err := Validate(r); err == nil {
				t.Fatal("Validate accepted game content")
			}
			stub := writeFile(t, filepath.Join(t.TempDir(), "stub.exe"), []byte("MZ"))
			out := filepath.Join(t.TempDir(), "bad.exe")
			if err := Pack(stub, r, out); err == nil {
				t.Fatal("Pack accepted game content")
			}
			assertAbsent(t, out)
		})
	}
}

func TestRejectFilesystemSymlinks(t *testing.T) {
	for _, kind := range []string{"file", "directory", "root", "ancestor", "dangling destination"} {
		t.Run(kind, func(t *testing.T) {
			r := fixture(t)
			outside := t.TempDir()
			link := filepath.Join(outside, "link")
			if err := os.Symlink(r, link); err != nil {
				t.Skipf("symlinks unavailable: %v", err)
			}
			defer os.Remove(link)
			switch kind {
			case "file":
				target := writeFile(t, filepath.Join(outside, "python.exe"), fixtureFiles()["python.exe"])
				if err := os.Remove(filepath.Join(r, "python.exe")); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, filepath.Join(r, "python.exe")); err != nil {
					t.Fatal(err)
				}
			case "directory":
				if err := os.Rename(filepath.Join(r, "Lib"), filepath.Join(outside, "Lib")); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(outside, "Lib"), filepath.Join(r, "Lib")); err != nil {
					t.Fatal(err)
				}
			case "root":
				r = link
			case "ancestor":
				if err := os.Rename(filepath.Join(r, "Lib"), filepath.Join(r, "new")); err != nil {
					t.Fatal(err)
				}
				// Put a complete valid runtime below the linked ancestor.
				child := fixture(t)
				if err := os.Rename(child, filepath.Join(r, "child")); err != nil {
					t.Fatal(err)
				}
				r = filepath.Join(link, "child")
			case "dangling destination":
				out, _ := packFixture(t)
				dest := filepath.Join(outside, "dangling")
				if err := os.Symlink(filepath.Join(outside, "missing"), dest); err != nil {
					t.Fatal(err)
				}
				if found, err := Extract(out, dest); !found || err == nil {
					t.Fatalf("Extract = %v, %v", found, err)
				}
				if _, err := os.Readlink(dest); err != nil {
					t.Fatal("destination link modified")
				}
				return
			}
			if err := Validate(r); err == nil {
				t.Fatal("Validate accepted symlink")
			}
			stub := writeFile(t, filepath.Join(outside, "stub.exe"), []byte("MZ"))
			out := filepath.Join(outside, "bad.exe")
			if err := Pack(stub, r, out); err == nil {
				t.Fatal("Pack accepted symlink")
			}
			assertAbsent(t, out)
		})
	}
}
