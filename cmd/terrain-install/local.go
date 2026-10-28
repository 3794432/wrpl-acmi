package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/maxsupermanhd/wrpl-inspector/v3/internal/terrainruntime"
	terrainres "github.com/maxsupermanhd/wrpl-inspector/v3/tools/terrain"
)

const pythonBootstrap = "import sys,runpy;sys.path.insert(0,sys.argv[1]);sys.argv=sys.argv[2:];runpy.run_path(sys.argv[0],run_name='__main__')"

func localGameRoot(choice, executable string) (string, error) {
	if choice == "" {
		choice = filepath.Dir(executable)
	}
	root, err := resolvePath(choice)
	if err != nil {
		return "", err
	}
	for _, item := range []struct {
		name      string
		directory bool
	}{{"levels", true}, {"aces.vromfs.bin", false}} {
		info, err := os.Stat(filepath.Join(root, item.name))
		if err != nil || info.IsDir() != item.directory {
			return "", fmt.Errorf("War Thunder root %q is missing %s; put this EXE in ./War Thunder/ beside levels/ and aces.vromfs.bin, or use -game <War Thunder directory>", root, item.name)
		}
	}
	return root, nil
}

func prepareLocalTerrain(ctx context.Context, opts options, stdout, stderr io.Writer) (string, func(), error) {
	exe, err := os.Executable()
	if err != nil {
		return "", nil, err
	}
	exe, err = resolvePath(exe)
	if err != nil {
		return "", nil, err
	}
	game, err := localGameRoot(opts.gameRoot, exe)
	if err != nil {
		return "", nil, err
	}
	cache := opts.cacheRoot
	if cache == "" {
		base, err := os.UserCacheDir()
		if err != nil {
			return "", nil, fmt.Errorf("locate local cache: %w", err)
		}
		cache = filepath.Join(base, "wrpl-acmi", "terrain")
	}
	cache, err = resolvePath(cache)
	if err != nil {
		return "", nil, err
	}
	if err = validatePathPair(game, cache); err != nil {
		return "", nil, err
	}
	destination, err := resolvePath(opts.destination)
	if err != nil {
		return "", nil, err
	}
	if err = validatePathPair(game, destination); err != nil {
		return "", nil, err
	}
	if err = validatePathPair(cache, destination); err != nil {
		return "", nil, err
	}
	id, err := terrainruntime.PayloadID(exe)
	if err != nil {
		return "", nil, fmt.Errorf("portable runtime missing or invalid; build this installer with build.ps1: %w", err)
	}
	if err = os.MkdirAll(cache, 0o755); err != nil {
		return "", nil, err
	}
	release, err := acquireConversionLock(filepath.Join(cache, "conversion.lock"))
	if err != nil {
		return "", nil, fmt.Errorf("another terrain conversion is running, or cache cannot be locked: %w", err)
	}
	ok := false
	defer func() {
		if !ok {
			release()
		}
	}()
	runtimeRoot := filepath.Join(cache, "runtime", id)
	if err = os.MkdirAll(filepath.Dir(runtimeRoot), 0o755); err != nil {
		return "", nil, err
	}
	if _, statErr := os.Stat(runtimeRoot); statErr == nil {
		if err = terrainruntime.ValidateEmbedded(exe, runtimeRoot); err != nil {
			quarantine := runtimeRoot + ".invalid-" + fmt.Sprint(time.Now().UnixNano())
			if !pathWithin(cache, runtimeRoot) || !pathWithin(cache, quarantine) {
				return "", nil, errors.New("unsafe runtime cache path")
			}
			if err = os.Rename(runtimeRoot, quarantine); err != nil {
				return "", nil, fmt.Errorf("quarantine damaged runtime: %w", err)
			}
			fmt.Fprintln(stdout, "Damaged runtime cache quarantined; restoring embedded runtime.")
		}
	} else if !os.IsNotExist(statErr) {
		return "", nil, statErr
	}
	if _, statErr := os.Stat(runtimeRoot); os.IsNotExist(statErr) {
		found, extractErr := terrainruntime.Extract(exe, runtimeRoot)
		if extractErr != nil {
			return "", nil, extractErr
		}
		if !found {
			return "", nil, errors.New("installer contains no portable runtime")
		}
	}
	gameHash := sha256.Sum256([]byte(strings.ToLower(game)))
	conversionID := terrainres.ConversionDigest()
	source := filepath.Join(cache, "maps", fmt.Sprintf("%x", gameHash[:8]), conversionID[:16])
	resolvedSource, err := resolvePath(source)
	if err != nil {
		return "", nil, err
	}
	if !pathWithin(cache, resolvedSource) {
		return "", nil, errors.New("conversion cache must not escape the configured cache root")
	}
	if err = validatePathPair(game, resolvedSource); err != nil {
		return "", nil, err
	}
	source = resolvedSource
	if err = os.MkdirAll(source, 0o755); err != nil {
		return "", nil, err
	}
	scripts, err := os.MkdirTemp(cache, "conversion-sources-")
	if err != nil {
		return "", nil, err
	}
	defer os.RemoveAll(scripts)
	if err = terrainres.MaterializeConversion(scripts); err != nil {
		return "", nil, err
	}
	logPath := filepath.Join(source, "conversion.log")
	log, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return "", nil, err
	}
	defer log.Close()
	fmt.Fprintf(stdout, "Game: %s\nConversion cache: %s\nLog: %s\nOpen-source licenses: %s\n", game, source, logPath, filepath.Join(runtimeRoot, "licenses"))
	resolution := opts.resolution
	if resolution == 0 {
		resolution = 4096
	}
	args := []string{"-I", "-B", "-u", "-X", "utf8", "-c", pythonBootstrap, filepath.Join(scripts, "converter"), filepath.Join(scripts, "converter", "local_install.py"),
		"--game", game, "--output", source, "--references", filepath.Join(scripts, "AtlasReferences.json"), "--geo-references", filepath.Join(scripts, "GeoReferences.json"), "--resolution", fmt.Sprint(resolution)}
	if opts.validateOnly {
		args = append(args, "--preflight")
	}
	fmt.Fprintf(log, "\n%s local conversion; game=%s; resolution=%d; preflight=%v\n", time.Now().UTC().Format(time.RFC3339), game, resolution, opts.validateOnly)
	err = runLocalPython(ctx, filepath.Join(runtimeRoot, "python.exe"), args, io.MultiWriter(stdout, log), io.MultiWriter(stderr, log))
	if err != nil {
		return "", nil, fmt.Errorf("local conversion failed; cache retained for retry; log %s: %w", logPath, err)
	}
	if opts.convertOnly {
		fmt.Fprintf(stdout, "Conversion complete; installation skipped. Source: %s\n", source)
	}
	ok = true
	return source, release, nil
}
