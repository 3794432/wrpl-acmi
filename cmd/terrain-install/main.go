package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/maxsupermanhd/wrpl-inspector/v3/internal/terrainbundle"
	terrainres "github.com/maxsupermanhd/wrpl-inspector/v3/tools/terrain"
)

const (
	installerName    = "wrpl-terrain-installer"
	profileAuto      = "auto"
	profile194       = "1.9.4"
	profile195       = "1.9.5"
	formatStateName  = "WarThunderMaps.format.json"
	installLogName   = "install.log"
	installWrapper   = "install-wrapper.ps1"
	defaultDestParts = "Tacview/Data/Terrain/Custom/WarThunderMaps"
)

// install-wrapper.ps1 is embedded so the installer remains one executable.
//
//go:embed install-wrapper.ps1
var wrapperSource []byte

type options struct {
	sourceRoot   string
	destination  string
	profile      string
	validateOnly bool
	atlasOnly    bool
	allowPartial bool
}

type dependencies struct {
	getenv      func(string) string
	mkdirTemp   func(string, string) (string, error)
	removeAll   func(string) error
	executable  func() (string, error)
	extract     func(string, string) (bool, error)
	materialize func(string) error
	run         func(context.Context, []string, io.Writer, io.Writer) error
	readVersion func(string) (string, error)
}

func defaultDependencies() dependencies {
	return dependencies{
		getenv:      os.Getenv,
		mkdirTemp:   os.MkdirTemp,
		removeAll:   os.RemoveAll,
		executable:  os.Executable,
		extract:     terrainbundle.Extract,
		materialize: terrainres.Materialize,
		run:         runPowerShell,
		readVersion: readExecutableFileVersion,
	}
}

type retainedError struct {
	TempDir string
	LogPath string
	Err     error
}

func (e *retainedError) Error() string {
	return fmt.Sprintf("%v; temporary installer files retained at %s (log: %s)", e.Err, e.TempDir, e.LogPath)
}

func (e *retainedError) Unwrap() error { return e.Err }

func main() {
	if err := runMain(context.Background(), os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func runMain(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	opts, err := parseOptions(args, os.Getenv, stderr)
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	if err != nil {
		return err
	}
	return runInstall(ctx, opts, defaultDependencies(), stdout, stderr)
}

func defaultDestination(getenv func(string) string) (string, error) {
	appData := strings.TrimSpace(getenv("APPDATA"))
	if appData == "" {
		return "", errors.New("APPDATA is not set; pass -destination <terrain-directory>")
	}
	return filepath.Join(appData, filepath.FromSlash(defaultDestParts)), nil
}

func parseOptions(args []string, getenv func(string) string, output io.Writer) (options, error) {
	var opts options
	defaultDest, _ := defaultDestination(getenv)
	fs := flag.NewFlagSet(installerName, flag.ContinueOnError)
	fs.SetOutput(output)
	fs.Usage = func() {
		fmt.Fprintf(output, "Usage: %s [options]\n\n", installerName)
		fmt.Fprintln(output, "Installs a converted War Thunder terrain set into Tacview using Windows PowerShell.")
		fmt.Fprintln(output, "Without -source, terrain is extracted from the payload appended to this executable.")
		fmt.Fprintln(output)
		fs.PrintDefaults()
	}
	fs.StringVar(&opts.sourceRoot, "source", "", "optional converted terrain root; defaults to the payload embedded in this executable")
	fs.StringVar(&opts.destination, "destination", defaultDest, "Tacview terrain destination (default: %APPDATA%\\Tacview\\Data\\Terrain\\Custom\\WarThunderMaps)")
	fs.StringVar(&opts.profile, "profile", profileAuto, "Tacview format: auto, 1.9.4, or 1.9.5")
	fs.BoolVar(&opts.validateOnly, "validate-only", false, "validate the source only; do not write the destination or select a format")
	fs.BoolVar(&opts.atlasOnly, "atlas", false, "install the technical Atlas layout and skip the geographic overlay")
	fs.BoolVar(&opts.allowPartial, "allow-partial", false, "allow synthetic test fixtures; intended for installer tests only")
	if err := fs.Parse(args); err != nil {
		return options{}, err
	}
	if fs.NArg() != 0 {
		return options{}, fmt.Errorf("unexpected positional arguments: %s", strings.Join(fs.Args(), " "))
	}
	opts.profile = strings.ToLower(strings.TrimSpace(opts.profile))
	switch opts.profile {
	case profileAuto, profile194, profile195:
	default:
		return options{}, fmt.Errorf("invalid -profile %q: use auto, 1.9.4, or 1.9.5", opts.profile)
	}
	opts.sourceRoot = strings.TrimSpace(opts.sourceRoot)
	opts.destination = strings.TrimSpace(opts.destination)
	if opts.destination == "" {
		return options{}, errors.New("destination is empty; pass -destination <terrain-directory> or set APPDATA")
	}
	return opts, nil
}

func runInstall(ctx context.Context, opts options, deps dependencies, stdout, stderr io.Writer) error {
	if deps.mkdirTemp == nil || deps.removeAll == nil || deps.run == nil || deps.materialize == nil {
		return errors.New("internal error: incomplete installer dependencies")
	}

	tempDir := ""
	preserve := false
	defer func() {
		if tempDir != "" && !preserve {
			_ = deps.removeAll(tempDir)
		}
	}()

	sourceRoot := opts.sourceRoot
	if sourceRoot == "" {
		var err error
		tempDir, err = deps.mkdirTemp("", "wrpl-terrain-installer-*")
		if err != nil {
			return fmt.Errorf("create temporary installer directory: %w", err)
		}
		exePath, err := deps.executable()
		if err != nil {
			return fmt.Errorf("locate this executable: %w", err)
		}
		payloadDir := filepath.Join(tempDir, "payload")
		found, err := deps.extract(exePath, payloadDir)
		if err != nil {
			return fmt.Errorf("extract embedded terrain payload: %w", err)
		}
		if !found {
			return errors.New("this executable contains no terrain payload; pass -source <converted-terrain>, or rebuild with build.ps1 -TerrainSource <converted-terrain> or -WithTerrain. A logic-only build does not contain War Thunder heights")
		}
		sourceRoot = payloadDir
	}

	var err error
	sourceRoot, err = resolvePath(sourceRoot)
	if err != nil {
		return fmt.Errorf("resolve source root: %w", err)
	}
	destination, err := resolvePath(opts.destination)
	if err != nil {
		return fmt.Errorf("resolve destination: %w", err)
	}
	if err := validatePathPair(sourceRoot, destination); err != nil {
		return err
	}
	if info, err := os.Stat(filepath.Join(sourceRoot, "maps")); err != nil {
		return fmt.Errorf("converted terrain root %s has no maps directory: %w", sourceRoot, err)
	} else if !info.IsDir() {
		return fmt.Errorf("converted terrain root %s has a maps entry that is not a directory", sourceRoot)
	}
	if info, err := os.Stat(destination); err == nil && !info.IsDir() {
		return fmt.Errorf("destination is not a directory: %s", destination)
	} else if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("inspect destination: %w", err)
	}

	targetProfile := profile194
	if !opts.validateOnly {
		var notice string
		targetProfile, notice, err = chooseProfile(opts.profile, deps.getenv, deps.readVersion)
		if err != nil {
			return err
		}
		if notice != "" {
			fmt.Fprintln(stdout, notice)
		}
	}

	if tempDir == "" {
		tempDir, err = deps.mkdirTemp("", "wrpl-terrain-installer-*")
		if err != nil {
			return fmt.Errorf("create temporary installer directory: %w", err)
		}
	}
	resourceDir := filepath.Join(tempDir, "terrain-scripts")
	if err := deps.materialize(resourceDir); err != nil {
		return fmt.Errorf("prepare embedded terrain scripts: %w", err)
	}
	installScript := filepath.Join(resourceDir, "Install-All-Terrains.ps1")
	formatScript := filepath.Join(resourceDir, "Set-Tacview-Terrain-Format.ps1")
	wrapperPath := filepath.Join(tempDir, installWrapper)
	if err := os.WriteFile(wrapperPath, wrapperSource, 0o600); err != nil {
		return fmt.Errorf("write temporary PowerShell wrapper: %w", err)
	}
	logPath := filepath.Join(tempDir, installLogName)
	logFile, err := os.Create(logPath)
	if err != nil {
		return fmt.Errorf("create installer log: %w", err)
	}
	defer logFile.Close()
	fmt.Fprintf(logFile, "%s\nSource: %s\nDestination: %s\nProfile: %s\n\n", installerName, sourceRoot, destination, targetProfile)

	fmt.Fprintf(stdout, "Source: %s\nDestination: %s\n", sourceRoot, destination)
	if opts.validateOnly {
		fmt.Fprintln(stdout, "Mode: validate source only (no destination writes, no format selection)")
	} else {
		fmt.Fprintf(stdout, "Profile: %s\n", targetProfile)
	}

	runStep := func(label string, args []string) error {
		fmt.Fprintln(stdout, label)
		out := io.MultiWriter(stdout, logFile)
		errOut := io.MultiWriter(stderr, logFile)
		if err := deps.run(ctx, args, out, errOut); err != nil {
			preserve = true
			return &retainedError{TempDir: tempDir, LogPath: logPath, Err: fmt.Errorf("%s: %w", label, err)}
		}
		return nil
	}

	validateArgs := buildWrapperArgs(wrapperPath, installScript, formatScript, sourceRoot, destination, targetProfile, true, opts.atlasOnly, opts.allowPartial)
	if err := runStep("Validating converted terrain source...", validateArgs); err != nil {
		return err
	}
	if opts.validateOnly {
		fmt.Fprintln(stdout, "Source validation completed; destination files and Tacview format were not changed.")
		return nil
	}

	currentProfile, err := installedFormatProfile(destination)
	if err != nil {
		return err
	}
	if currentProfile == profile194 {
		restoreArgs := buildRestoreArgs(wrapperPath, installScript, formatScript, sourceRoot, destination, opts.allowPartial)
		if err := runStep("Restoring the installed 1.9.5 XML layout before reinstalling...", restoreArgs); err != nil {
			return err
		}
	}

	installArgs := buildWrapperArgs(wrapperPath, installScript, formatScript, sourceRoot, destination, targetProfile, false, opts.atlasOnly, opts.allowPartial)
	if err := runStep("Installing terrain and selecting the Tacview format...", installArgs); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Terrain installation completed with profile %s. Restart Tacview.\n", targetProfile)
	return nil
}

func buildWrapperArgs(wrapperPath, installScript, formatScript, sourceRoot, destination, profile string, validateOnly, atlasOnly, allowPartial bool) []string {
	scriptArgs := []string{
		"-InstallScript", installScript,
		"-FormatScript", formatScript,
		"-SourceRoot", sourceRoot,
		"-Destination", destination,
		"-Profile", profile,
	}
	if validateOnly {
		scriptArgs = append(scriptArgs, "-ValidateOnly")
	}
	if atlasOnly {
		scriptArgs = append(scriptArgs, "-AtlasOnly")
	}
	if allowPartial {
		scriptArgs = append(scriptArgs, "-AllowPartial")
	}
	return powershellArgs(wrapperPath, scriptArgs)
}

func buildRestoreArgs(wrapperPath, installScript, formatScript, sourceRoot, destination string, allowPartial bool) []string {
	scriptArgs := []string{
		"-InstallScript", installScript,
		"-FormatScript", formatScript,
		"-SourceRoot", sourceRoot,
		"-Destination", destination,
		"-Profile", profile195,
		"-RestoreOnly",
	}
	if allowPartial {
		scriptArgs = append(scriptArgs, "-AllowPartial")
	}
	return powershellArgs(wrapperPath, scriptArgs)
}

func powershellArgs(script string, scriptArgs []string) []string {
	args := []string{"-NoProfile", "-ExecutionPolicy", "Bypass", "-File", script}
	return append(args, scriptArgs...)
}

func installedFormatProfile(destination string) (string, error) {
	path := filepath.Join(destination, formatStateName)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read installed terrain format state: %w", err)
	}
	var state struct {
		Profile string `json:"profile"`
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return "", fmt.Errorf("parse installed terrain format state %s: %w", path, err)
	}
	switch state.Profile {
	case profile194, profile195:
		return state.Profile, nil
	case "":
		return "", nil
	default:
		return "", fmt.Errorf("installed terrain format state %s has unknown profile %q", path, state.Profile)
	}
}

func chooseProfile(choice string, getenv func(string) string, readVersion func(string) (string, error)) (string, string, error) {
	switch choice {
	case profile194, profile195:
		return choice, "", nil
	case profileAuto:
	default:
		return "", "", fmt.Errorf("invalid -profile %q", choice)
	}
	if readVersion == nil {
		return "", "", errors.New("internal error: executable version reader is unavailable")
	}

	var bases []string
	seen := map[string]bool{}
	for _, envName := range []string{"ProgramFiles(x86)", "ProgramFiles"} {
		base := strings.TrimSpace(getenv(envName))
		if base == "" {
			continue
		}
		clean := filepath.Clean(base)
		key := strings.ToLower(clean)
		if !seen[key] {
			seen[key] = true
			bases = append(bases, clean)
		}
	}
	foundAt := ""
	var versionErr error
	for _, base := range bases {
		exePath := filepath.Join(base, "Tacview", "Tacview64.exe")
		info, err := os.Stat(exePath)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		foundAt = exePath
		version, err := readVersion(exePath)
		if err != nil {
			versionErr = err
			continue
		}
		profile, err := profileForVersion(version)
		if err != nil {
			versionErr = fmt.Errorf("invalid FileVersion %q: %w", version, err)
			continue
		}
		return profile, fmt.Sprintf("Tacview %s detected; selected the %s terrain profile.", version, profile), nil
	}
	if foundAt != "" {
		return "", "", fmt.Errorf("Tacview was found at %s but its FileVersion could not be used; pass -profile 1.9.4 or -profile 1.9.5: %w", foundAt, versionErr)
	}
	return profile194, "Tacview was not found under Program Files; defaulting to the 1.9.4 terrain profile. Pass -profile to override.", nil
}

func profileForVersion(version string) (string, error) {
	parts := strings.Split(strings.TrimSpace(version), ".")
	if len(parts) < 2 {
		return "", fmt.Errorf("invalid numeric FileVersion %q", version)
	}
	numbers := make([]int, 3)
	for i := 0; i < len(numbers) && i < len(parts); i++ {
		part := strings.TrimSpace(parts[i])
		value, err := strconv.Atoi(part)
		if err != nil || value < 0 {
			return "", fmt.Errorf("invalid numeric FileVersion %q", version)
		}
		numbers[i] = value
	}
	major, minor, patch := numbers[0], numbers[1], numbers[2]
	if major > 1 || (major == 1 && (minor > 9 || (minor == 9 && patch >= 5))) {
		return profile195, nil
	}
	return profile194, nil
}

func validatePathPair(source, destination string) error {
	source, err := resolvePath(source)
	if err != nil {
		return fmt.Errorf("resolve source path: %w", err)
	}
	destination, err = resolvePath(destination)
	if err != nil {
		return fmt.Errorf("resolve destination path: %w", err)
	}
	if strings.EqualFold(source, destination) || pathWithin(source, destination) || pathWithin(destination, source) {
		return fmt.Errorf("source and destination must not contain one another: source=%s destination=%s", source, destination)
	}
	return nil
}

func pathWithin(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	if err != nil || filepath.IsAbs(rel) {
		return false
	}
	if rel == "." {
		return true
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func resolvePath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	current := abs
	var missing []string
	for {
		resolved, err := filepath.EvalSymlinks(current)
		if err == nil {
			for i := len(missing) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, missing[i])
			}
			return filepath.Clean(resolved), nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return filepath.Clean(abs), nil
		}
		missing = append(missing, filepath.Base(current))
		current = parent
	}
}

func runPowerShell(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	return runPowerShellCommand(ctx, args, stdout, stderr)
}
