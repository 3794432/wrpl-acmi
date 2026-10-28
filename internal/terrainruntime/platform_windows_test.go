package terrainruntime

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
)

func TestRejectWindowsJunctions(t *testing.T) {
	r := fixture(t)
	parent := t.TempDir()
	link := filepath.Join(parent, "junction")
	command := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", `$ErrorActionPreference='Stop'; New-Item -ItemType Junction -Path $env:WRPL_TEST_LINK -Target $env:WRPL_TEST_TARGET | Out-Null`)
	command.Env = append(os.Environ(), "WRPL_TEST_LINK="+link, "WRPL_TEST_TARGET="+r)
	if output, err := command.CombinedOutput(); err != nil {
		t.Skipf("junction creation unavailable: %v: %s", err, output)
	}
	defer os.Remove(link)
	info, err := os.Lstat(link)
	if err != nil {
		t.Fatal(err)
	}
	if !isReparse(info) {
		t.Fatal("junction reparse attribute not detected")
	}
	if err := Validate(link); err == nil {
		t.Fatal("Validate accepted junction root")
	}
	stub := writeFile(t, filepath.Join(parent, "stub.exe"), []byte("MZ"))
	if err := Pack(stub, link, filepath.Join(parent, "bad.exe")); err == nil {
		t.Fatal("Pack accepted junction root")
	}
	out, _ := packFixture(t)
	if found, err := Extract(out, filepath.Join(link, "child")); !found || err == nil {
		t.Fatalf("Extract through junction = %v, %v", found, err)
	}
	assertAbsent(t, filepath.Join(r, "child"))
	// A junction inside an otherwise regular runtime is rejected too.
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(r, "Lib"), filepath.Join(parent, "Lib")); err != nil {
		t.Fatal(err)
	}
	link = filepath.Join(r, "Lib")
	command.Env = append(os.Environ(), "WRPL_TEST_LINK="+link, "WRPL_TEST_TARGET="+filepath.Join(parent, "Lib"))
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("create inner junction: %v: %s", err, output)
	}
	defer os.Remove(link)
	if err := Validate(r); err == nil {
		t.Fatal("Validate accepted inner junction")
	}
}

type reparseInfo struct{ os.FileInfo }

func (reparseInfo) Sys() any {
	return &syscall.Win32FileAttributeData{FileAttributes: syscall.FILE_ATTRIBUTE_REPARSE_POINT}
}

func TestReparseAttributeWithoutSymlinkMode(t *testing.T) {
	info, err := os.Stat(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	r := reparseInfo{info}
	if r.Mode()&os.ModeSymlink != 0 {
		t.Fatal("fixture unexpectedly has symlink mode")
	}
	if !unsafeInfo(r) {
		t.Fatal("reparse point without symlink mode was accepted")
	}
}
