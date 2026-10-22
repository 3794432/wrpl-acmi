package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/maxsupermanhd/wrpl-inspector/v3/internal/terrainbundle"
	terrainres "github.com/maxsupermanhd/wrpl-inspector/v3/tools/terrain"
)

func TestParseOptionsDefaultsAndFlags(t *testing.T) {
	appData := t.TempDir()
	env := func(name string) string {
		if name == "APPDATA" {
			return appData
		}
		return ""
	}
	got, err := parseOptions(nil, env, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	wantDest := filepath.Join(appData, "Tacview", "Data", "Terrain", "Custom", "WarThunderMaps")
	if got.destination != wantDest || got.profile != profileAuto || got.validateOnly || got.atlasOnly || got.allowPartial {
		t.Fatalf("defaults = %+v", got)
	}

	got, err = parseOptions([]string{
		"-source", `C:\terrain`, "-destination", `D:\Tacview maps`, "-profile", "1.9.5",
		"-validate-only", "-atlas", "-allow-partial",
	}, func(string) string { return "" }, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if got.sourceRoot != `C:\terrain` || got.destination != `D:\Tacview maps` || got.profile != profile195 ||
		!got.validateOnly || !got.atlasOnly || !got.allowPartial {
		t.Fatalf("parsed options = %+v", got)
	}
}

func TestParseOptionsRejectsInvalidInput(t *testing.T) {
	for _, args := range [][]string{
		{"-profile", "1.9.6"},
		{"-unknown"},
		{"positional"},
	} {
		if _, err := parseOptions(args, func(string) string { return t.TempDir() }, io.Discard); err == nil {
			t.Fatalf("parseOptions(%q) accepted invalid input", args)
		}
	}
	if _, err := parseOptions(nil, func(string) string { return "" }, io.Discard); err == nil || !strings.Contains(err.Error(), "APPDATA") {
		t.Fatalf("missing APPDATA error = %v", err)
	}
	if _, err := parseOptions([]string{"-destination", t.TempDir()}, func(string) string { return "" }, io.Discard); err != nil {
		t.Fatalf("explicit destination unexpectedly needed APPDATA: %v", err)
	}
	if _, err := parseOptions([]string{"-h"}, func(string) string { return t.TempDir() }, io.Discard); !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("-h error = %v, want flag.ErrHelp", err)
	}
}

func TestBuildPowerShellArguments(t *testing.T) {
	wrapper := filepath.Join(t.TempDir(), "wrapper with space.ps1")
	install := filepath.Join(t.TempDir(), "Install-All-Terrains.ps1")
	format := filepath.Join(t.TempDir(), "Set-Tacview-Terrain-Format.ps1")
	source := filepath.Join(t.TempDir(), "source terrain")
	destination := filepath.Join(t.TempDir(), "target terrain")

	args := buildWrapperArgs(wrapper, install, format, source, destination, profile194, false, true, true)
	wantPrefix := []string{"-NoProfile", "-ExecutionPolicy", "Bypass", "-File", wrapper}
	if !reflect.DeepEqual(args[:len(wantPrefix)], wantPrefix) {
		t.Fatalf("PowerShell prefix = %q, want %q", args[:len(wantPrefix)], wantPrefix)
	}
	checks := map[string]string{
		"-InstallScript": install,
		"-FormatScript":  format,
		"-SourceRoot":    source,
		"-Destination":   destination,
		"-Profile":       profile194,
	}
	for name, want := range checks {
		if got, ok := argumentValue(args, name); !ok || got != want {
			t.Fatalf("%s = %q, %v; want %q", name, got, ok, want)
		}
	}
	for _, name := range []string{"-AtlasOnly", "-AllowPartial"} {
		if !hasArgument(args, name) {
			t.Fatalf("missing %s in %q", name, args)
		}
	}
	if hasArgument(args, "-ValidateOnly") || hasArgument(args, "-AtlasReferences") {
		t.Fatalf("unexpected argument in %q; source references must remain authoritative", args)
	}

	restore := buildRestoreArgs(wrapper, install, format, source, destination, true)
	if argumentFor(restore, "-File") != wrapper {
		t.Fatalf("restore script = %q, want wrapper %q", argumentFor(restore, "-File"), wrapper)
	}
	if got, ok := argumentValue(restore, "-Profile"); !ok || got != profile195 {
		t.Fatalf("restore profile = %q, %v", got, ok)
	}
	if !hasArgument(restore, "-RestoreOnly") || !hasArgument(restore, "-AllowPartial") || hasArgument(restore, "-ValidateOnly") {
		t.Fatalf("restore args = %q", restore)
	}
}

func TestChooseProfile(t *testing.T) {
	for _, test := range []struct {
		choice string
		want   string
	}{
		{profile194, profile194},
		{profile195, profile195},
	} {
		got, notice, err := chooseProfile(test.choice, func(string) string { return "" }, func(string) (string, error) {
			t.Fatal("explicit profile invoked version detection")
			return "", nil
		})
		if err != nil || got != test.want || notice != "" {
			t.Fatalf("chooseProfile(%s) = %q, %q, %v", test.choice, got, notice, err)
		}
	}

	for _, test := range []struct {
		version string
		want    string
	}{
		{"1.9.4.0", profile194},
		{"1.9.4", profile194},
		{"1.9.5.0", profile195},
		{"1.9.5.12", profile195},
		{"2.0.0.0", profile195},
		{"1.9.3.9", profile194},
	} {
		base := t.TempDir()
		exe := filepath.Join(base, "Tacview", "Tacview64.exe")
		if err := os.MkdirAll(filepath.Dir(exe), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(exe, []byte("test"), 0o600); err != nil {
			t.Fatal(err)
		}
		env := func(name string) string {
			if name == "ProgramFiles(x86)" {
				return base
			}
			return ""
		}
		got, notice, err := chooseProfile(profileAuto, env, func(path string) (string, error) {
			if path != exe {
				t.Fatalf("version probe path = %q, want %q", path, exe)
			}
			return test.version, nil
		})
		if err != nil || got != test.want || !strings.Contains(notice, test.want) {
			t.Fatalf("version %s: profile=%q notice=%q err=%v", test.version, got, notice, err)
		}
	}

	got, notice, err := chooseProfile(profileAuto, func(string) string { return "" }, func(string) (string, error) {
		t.Fatal("missing Tacview invoked version reader")
		return "", nil
	})
	if err != nil || got != profile194 || !strings.Contains(notice, "not found") {
		t.Fatalf("missing Tacview = %q, %q, %v", got, notice, err)
	}

	base := t.TempDir()
	exe := filepath.Join(base, "Tacview", "Tacview64.exe")
	if err := os.MkdirAll(filepath.Dir(exe), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exe, []byte("test"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := chooseProfile(profileAuto, func(name string) string {
		if name == "ProgramFiles" {
			return base
		}
		return ""
	}, func(string) (string, error) { return "", errors.New("version unavailable") }); err == nil || !strings.Contains(err.Error(), "-profile") {
		t.Fatalf("version read failure = %v", err)
	}
	if _, err := profileForVersion("1.x.5"); err == nil {
		t.Fatal("malformed FileVersion was accepted")
	}
}

func TestValidatePathPairRejectsContainment(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	destination := filepath.Join(root, "destination")
	for _, path := range []string{source, destination, filepath.Join(source, "child")} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := validatePathPair(source, destination); err != nil {
		t.Fatalf("sibling paths rejected: %v", err)
	}
	if err := validatePathPair(source, filepath.Join(source, "..", "destination")); err != nil {
		t.Fatalf("normalized sibling paths rejected: %v", err)
	}
	for _, pair := range [][2]string{
		{source, source},
		{source, filepath.Join(source, "child")},
		{filepath.Join(source, "child"), source},
		{root, source},
		{source, root},
	} {
		if err := validatePathPair(pair[0], pair[1]); err == nil {
			t.Fatalf("overlapping paths accepted: %q -> %q", pair[0], pair[1])
		}
	}
}

func TestValidateOnlyRunsOnlySourceValidation(t *testing.T) {
	source, destination := makeFixturePaths(t)
	state := `{"profile":"1.9.4"}`
	if err := os.WriteFile(filepath.Join(destination, formatStateName), []byte(state), 0o600); err != nil {
		t.Fatal(err)
	}
	var calls [][]string
	removed := ""
	deps := fakeDependencies(func(_ context.Context, args []string, _, _ io.Writer) error {
		calls = append(calls, append([]string(nil), args...))
		return nil
	})
	deps.removeAll = func(path string) error { removed = path; return nil }
	opts := options{sourceRoot: source, destination: destination, profile: profileAuto, validateOnly: true, allowPartial: true}
	if err := runInstall(context.Background(), opts, deps, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 {
		t.Fatalf("validate-only made %d PowerShell calls, want 1: %q", len(calls), calls)
	}
	if !hasArgument(calls[0], "-ValidateOnly") || hasArgument(calls[0], "-AtlasOnly") {
		t.Fatalf("validate-only args = %q", calls[0])
	}
	if script := argumentFor(calls[0], "-File"); filepath.Base(script) != installWrapper {
		t.Fatalf("validate-only script = %q", script)
	}
	if removed == "" {
		t.Fatal("successful validate-only did not clean its temporary directory")
	}
}

func TestNormalInstallValidatesThenRestores194ThenInstalls(t *testing.T) {
	source, destination := makeFixturePaths(t)
	if err := os.WriteFile(filepath.Join(destination, formatStateName), []byte(`{"profile":"1.9.4"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var calls [][]string
	deps := fakeDependencies(func(_ context.Context, args []string, _, _ io.Writer) error {
		calls = append(calls, append([]string(nil), args...))
		return nil
	})
	removed := ""
	deps.removeAll = func(path string) error { removed = path; return nil }
	opts := options{sourceRoot: source, destination: destination, profile: profile194, allowPartial: true}
	if err := runInstall(context.Background(), opts, deps, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 3 {
		t.Fatalf("normal reinstall made %d calls, want 3:\n%s", len(calls), formatCalls(calls))
	}
	if filepath.Base(argumentFor(calls[0], "-File")) != installWrapper || !hasArgument(calls[0], "-ValidateOnly") {
		t.Fatalf("first call is not source validation: %q", calls[0])
	}
	if filepath.Base(argumentFor(calls[1], "-File")) != installWrapper ||
		!hasArgument(calls[1], "-RestoreOnly") || argumentFor(calls[1], "-Profile") != profile195 {
		t.Fatalf("second call is not the native restoration: %q", calls[1])
	}
	// Windows temp paths can use an 8.3 alias; the installer resolves it before
	// launching PowerShell. Verify directory identity rather than its spelling.
	wantDir, err := os.Stat(destination)
	if err != nil {
		t.Fatal(err)
	}
	gotDir, err := os.Stat(argumentFor(calls[1], "-Destination"))
	if err != nil || !os.SameFile(wantDir, gotDir) {
		t.Fatalf("restoration destination does not identify %q: %v", destination, err)
	}
	if filepath.Base(argumentFor(calls[2], "-File")) != installWrapper || hasArgument(calls[2], "-ValidateOnly") ||
		argumentFor(calls[2], "-Profile") != profile194 {
		t.Fatalf("third call is not the requested reinstall: %q", calls[2])
	}
	for _, call := range calls {
		if hasArgument(call, "-AtlasReferences") {
			t.Fatalf("source references were overridden by the embedded copy: %q", call)
		}
	}
	if removed == "" {
		t.Fatal("successful install did not clean its temporary directory")
	}
}

func TestSourceValidationFailureDoesNotRestoreOrInstall(t *testing.T) {
	source, destination := makeFixturePaths(t)
	if err := os.WriteFile(filepath.Join(destination, formatStateName), []byte(`{"profile":"1.9.4"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var calls [][]string
	deps := fakeDependencies(func(_ context.Context, args []string, _, _ io.Writer) error {
		calls = append(calls, append([]string(nil), args...))
		return errors.New("validation failed")
	})
	opts := options{sourceRoot: source, destination: destination, profile: profile194, allowPartial: true}
	err := runInstall(context.Background(), opts, deps, io.Discard, io.Discard)
	if err == nil {
		t.Fatal("validation failure returned nil")
	}
	var retained *retainedError
	if errors.As(err, &retained) {
		defer os.RemoveAll(retained.TempDir)
	}
	if len(calls) != 1 || !hasArgument(calls[0], "-ValidateOnly") {
		t.Fatalf("validation failure continued into restore/install: %s", formatCalls(calls))
	}
}

func TestWrapperFailureRetainsTempLogAndError(t *testing.T) {
	source, destination := makeFixturePaths(t)
	var removalCalled bool
	deps := fakeDependencies(func(_ context.Context, args []string, _, stderr io.Writer) error {
		wrapperPath := argumentFor(args, "-File")
		wrapper, err := os.ReadFile(wrapperPath)
		if err != nil {
			t.Fatalf("read temporary wrapper: %v", err)
		}
		if !bytes.Contains(wrapper, []byte("Source validation only")) {
			t.Fatal("temporary wrapper does not document validate-only semantics")
		}
		fmt.Fprintln(stderr, "simulated wrapper failure")
		return errors.New("exit status 1")
	})
	deps.removeAll = func(string) error { removalCalled = true; return nil }
	opts := options{sourceRoot: source, destination: destination, profile: profile194, validateOnly: true, allowPartial: true}
	err := runInstall(context.Background(), opts, deps, io.Discard, io.Discard)
	if err == nil {
		t.Fatal("wrapper failure returned nil")
	}
	var retained *retainedError
	if !errors.As(err, &retained) {
		t.Fatalf("error type = %T, want *retainedError: %v", err, err)
	}
	defer os.RemoveAll(retained.TempDir)
	if removalCalled {
		t.Fatal("failed installation removed its temporary directory")
	}
	if info, statErr := os.Stat(retained.TempDir); statErr != nil || !info.IsDir() {
		t.Fatalf("retained temp directory is missing: %v", statErr)
	}
	logData, readErr := os.ReadFile(retained.LogPath)
	if readErr != nil {
		t.Fatalf("read retained log: %v", readErr)
	}
	if !strings.Contains(string(logData), "simulated wrapper failure") || !strings.Contains(err.Error(), "retained") {
		t.Fatalf("retained diagnostics missing; log=%q err=%v", logData, err)
	}
}

func TestNoPayloadWithoutSourceExplainsBuildOptions(t *testing.T) {
	called := false
	deps := fakeDependencies(func(context.Context, []string, io.Writer, io.Writer) error {
		called = true
		return nil
	})
	deps.extract = func(string, string) (bool, error) { return false, nil }
	opts := options{destination: filepath.Join(t.TempDir(), "destination"), profile: profile194}
	err := runInstall(context.Background(), opts, deps, io.Discard, io.Discard)
	if err == nil || called {
		t.Fatalf("missing payload: err=%v called=%v", err, called)
	}
	for _, text := range []string{"-source", "-TerrainSource", "-WithTerrain", "no terrain payload"} {
		if !strings.Contains(err.Error(), text) {
			t.Fatalf("missing-payload error lacks %q: %v", text, err)
		}
	}
}

func TestEmbeddedTerrainResourcesAreExact(t *testing.T) {
	want := []string{
		"Install-All-Terrains.ps1",
		"Install-Geographic-Terrains.ps1",
		"Terrain-Format.ps1",
		"Terrain-194-Encoding.ps1",
		"Set-Tacview-Terrain-Format.ps1",
		"AtlasReferences.json",
		"GeoReferences.json",
	}
	if got := terrainres.Names(); !reflect.DeepEqual(got, want) {
		t.Fatalf("resource names = %q, want %q", got, want)
	}
	directory := t.TempDir()
	if err := terrainres.Materialize(directory); err != nil {
		t.Fatal(err)
	}
	for _, name := range want {
		data, err := os.ReadFile(filepath.Join(directory, name))
		if err != nil || len(data) == 0 {
			t.Fatalf("materialized %s: bytes=%d err=%v", name, len(data), err)
		}
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != len(want) {
		t.Fatalf("materialized %d files, want %d", len(entries), len(want))
	}
}

func TestInstalledFormatProfile(t *testing.T) {
	destination := t.TempDir()
	if got, err := installedFormatProfile(destination); err != nil || got != "" {
		t.Fatalf("missing state = %q, %v", got, err)
	}
	statePath := filepath.Join(destination, formatStateName)
	if err := os.WriteFile(statePath, []byte(`{"profile":"1.9.5"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := installedFormatProfile(destination); err != nil || got != profile195 {
		t.Fatalf("state = %q, %v", got, err)
	}
	if err := os.WriteFile(statePath, []byte(`{"profile":"future"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := installedFormatProfile(destination); err == nil {
		t.Fatal("unknown state profile was accepted")
	}
}

func TestSyntheticPowerShellInstallProfiles(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("terrain installation requires Windows PowerShell")
	}
	source, err := filepath.Abs(filepath.Join("..", "..", "tools", "terrain", "smalltests", "geographic", "fixture"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(source, "AtlasReferences.json")); err != nil {
		t.Skip("synthetic geographic fixture has not been generated")
	}
	// Use a workspace directory because this sandbox blocks PowerShell renames
	// below the packaged temp directory. The test still never touches APPDATA.
	testRoot, err := os.MkdirTemp(filepath.Join("..", ".."), ".terrain-install-test-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(testRoot) })
	destination := filepath.Join(testRoot, "Tacview", "Data", "Terrain", "Custom", "WarThunderMaps")
	var output bytes.Buffer
	deps := defaultDependencies()
	deps.getenv = func(string) string { return "" }

	validate := options{sourceRoot: source, destination: destination, profile: profile194, validateOnly: true, allowPartial: true}
	if err := runInstall(context.Background(), validate, deps, &output, &output); err != nil {
		t.Fatalf("validate-only failed: %v\n%s", err, output.String())
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatalf("validate-only created the destination: %v", err)
	}

	for _, profile := range []string{profile194, profile194, profile195} {
		output.Reset()
		opts := options{sourceRoot: source, destination: destination, profile: profile, allowPartial: true}
		if err := runInstall(context.Background(), opts, deps, &output, &output); err != nil {
			t.Fatalf("install profile %s failed: %v\n%s", profile, err, output.String())
		}
		stateData, err := os.ReadFile(filepath.Join(destination, formatStateName))
		if err != nil {
			t.Fatalf("read format state after %s: %v", profile, err)
		}
		var state struct {
			Profile string `json:"profile"`
		}
		if err := json.Unmarshal(stateData, &state); err != nil || state.Profile != profile {
			t.Fatalf("state after %s has profile %q, err=%v: %s", profile, state.Profile, err, stateData)
		}
		for _, id := range []string{"geo_alpha", "geo_beta"} {
			original, err := os.ReadFile(filepath.Join(source, "maps", id, id+".data"))
			if err != nil {
				t.Fatal(err)
			}
			installed, err := os.ReadFile(filepath.Join(destination, id, id+".data"))
			if err != nil {
				t.Fatalf("read installed %s raster: %v", id, err)
			}
			if !bytes.Equal(original, installed) {
				t.Fatalf("canonical raster changed after profile %s for %s", profile, id)
			}
		}
	}
}

func TestEmbeddedPayloadBecomesSourceWithoutOverridingItsReferences(t *testing.T) {
	source, err := filepath.Abs(filepath.Join("..", "..", "tools", "terrain", "smalltests", "geographic", "fixture"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(source, "AtlasReferences.json")); err != nil {
		t.Skip("synthetic geographic fixture has not been generated")
	}
	testRoot, err := os.MkdirTemp(filepath.Join("..", ".."), ".terrain-payload-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(testRoot)
	stub := filepath.Join(testRoot, "stub.exe")
	if err := os.WriteFile(stub, []byte("synthetic executable stub"), 0o700); err != nil {
		t.Fatal(err)
	}
	packed := filepath.Join(testRoot, "wrpl-terrain-installer.exe")
	if err := terrainbundle.Pack(stub, source, packed); err != nil {
		t.Fatalf("pack synthetic terrain payload: %v", err)
	}
	// Payload extraction publishes with a directory rename. Keep its temporary
	// files in the workspace so this test also runs under restricted sandboxes.
	t.Setenv("TEMP", testRoot)
	t.Setenv("TMP", testRoot)

	capturedSource := ""
	deps := defaultDependencies()
	deps.getenv = func(string) string { return "" }
	deps.executable = func() (string, error) { return packed, nil }
	deps.run = func(_ context.Context, args []string, _, _ io.Writer) error {
		capturedSource = argumentFor(args, "-SourceRoot")
		for _, name := range []string{"AtlasReferences.json", "GeoReferences.json"} {
			fromPayload, err := os.ReadFile(filepath.Join(capturedSource, name))
			if err != nil {
				t.Fatalf("payload is missing %s: %v", name, err)
			}
			fromFixture, err := os.ReadFile(filepath.Join(source, name))
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(fromPayload, fromFixture) {
				t.Fatalf("payload %s does not match the supplied source reference", name)
			}
		}
		return nil
	}
	opts := options{destination: filepath.Join(testRoot, "destination"), profile: profile194, validateOnly: true}
	if err := runInstall(context.Background(), opts, deps, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	if filepath.Base(capturedSource) != "payload" {
		t.Fatalf("embedded source root = %q, want the temporary payload directory", capturedSource)
	}
	if _, err := os.Stat(capturedSource); !os.IsNotExist(err) {
		t.Fatalf("temporary payload was not removed after success: %v", err)
	}
	if _, err := os.Stat(opts.destination); !os.IsNotExist(err) {
		t.Fatalf("validate-only payload path wrote the destination: %v", err)
	}
}

func makeFixturePaths(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	source := filepath.Join(root, "source")
	destination := filepath.Join(root, "destination")
	if err := os.MkdirAll(filepath.Join(source, "maps"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(destination, 0o755); err != nil {
		t.Fatal(err)
	}
	return source, destination
}

func fakeDependencies(run func(context.Context, []string, io.Writer, io.Writer) error) dependencies {
	return dependencies{
		getenv:      func(string) string { return "" },
		mkdirTemp:   os.MkdirTemp,
		removeAll:   os.RemoveAll,
		executable:  os.Executable,
		extract:     func(string, string) (bool, error) { return false, nil },
		materialize: terrainres.Materialize,
		run:         run,
		readVersion: func(string) (string, error) { return "1.9.5.0", nil },
	}
}

func hasArgument(args []string, name string) bool {
	_, ok := argumentValue(args, name)
	return ok
}

func argumentValue(args []string, name string) (string, bool) {
	for i, arg := range args {
		if arg == name {
			if i+1 >= len(args) {
				return "", true
			}
			return args[i+1], true
		}
	}
	return "", false
}

func argumentFor(args []string, name string) string {
	value, _ := argumentValue(args, name)
	return value
}

func formatCalls(calls [][]string) string {
	var lines []string
	for _, call := range calls {
		lines = append(lines, strings.Join(call, " "))
	}
	return strings.Join(lines, "\n")
}
