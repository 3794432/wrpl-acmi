package terrainruntime

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestValidateEmbeddedPinsMutableCacheManifest(t *testing.T) {
	t.Parallel()
	exe, _ := packFixture(t)
	dest := filepath.Join(t.TempDir(), "runtime")
	if found, err := Extract(exe, dest); !found || err != nil {
		t.Fatalf("Extract = %v, %v", found, err)
	}
	if err := ValidateEmbedded(exe, dest); err != nil {
		t.Fatalf("valid embedded runtime: %v", err)
	}
	files := fixtureFiles()
	files["python.exe"] = bytes.Repeat([]byte("!"), len(files["python.exe"]))
	writeFile(t, filepath.Join(dest, "python.exe"), files["python.exe"])
	writeFile(t, filepath.Join(dest, ManifestName), manifestFor(t, files))
	if err := Validate(dest); err != nil {
		t.Fatalf("self-consistent fixture should pass unpinned validation: %v", err)
	}
	if err := ValidateEmbedded(exe, dest); err == nil {
		t.Fatal("accepted substituted executable and matching attacker manifest")
	}
}

func TestValidateEmbeddedRejectsBadCacheAndPayload(t *testing.T) {
	t.Parallel()
	exe, _ := packFixture(t)
	for _, kind := range []string{"tampered file", "extra file", "missing file", "changed manifest bytes"} {
		t.Run(kind, func(t *testing.T) {
			dest := filepath.Join(t.TempDir(), "runtime")
			if found, err := Extract(exe, dest); !found || err != nil {
				t.Fatalf("Extract = %v, %v", found, err)
			}
			switch kind {
			case "tampered file":
				writeFile(t, filepath.Join(dest, "python.exe"), []byte("MZ changed"))
			case "extra file":
				writeFile(t, filepath.Join(dest, "cache.pyc"), []byte("cache"))
			case "missing file":
				if err := os.Remove(filepath.Join(dest, "python.exe")); err != nil {
					t.Fatal(err)
				}
			case "changed manifest bytes":
				data, err := os.ReadFile(filepath.Join(dest, ManifestName))
				if err != nil {
					t.Fatal(err)
				}
				writeFile(t, filepath.Join(dest, ManifestName), append(data, '\n'))
			}
			if err := ValidateEmbedded(exe, dest); err == nil {
				t.Fatal("invalid cache accepted")
			}
		})
	}
	plain := writeFile(t, filepath.Join(t.TempDir(), "plain.exe"), []byte("MZ"))
	if err := ValidateEmbedded(plain, fixture(t)); !errors.Is(err, ErrNoPayload) {
		t.Fatalf("missing payload: %v", err)
	}
	data, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	data[len(data)-1] ^= 1
	corrupt := writeFile(t, filepath.Join(t.TempDir(), "corrupt.exe"), data)
	if err := ValidateEmbedded(corrupt, fixture(t)); err == nil {
		t.Fatal("corrupt archive accepted")
	}
}
