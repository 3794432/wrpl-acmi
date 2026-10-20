package main

import (
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/maxsupermanhd/wrpl-inspector/v3/internal/terrainmap"
)

func main() { os.Exit(run(os.Args[1:])) }

func run(args []string) int {
	flags := flag.NewFlagSet("acmi-mapid", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	in := flags.String("in", "", "input .acmi file or directory (may also be supplied as positional paths)")
	mapFlag := flags.String("map", "", "explicit canonical map id or WarThunder.<name>")
	overwrite := flags.Bool("overwrite", false, "atomically replace an existing output")
	flags.Usage = func() {
		fmt.Fprintln(flags.Output(), "usage: acmi-mapid [-map ID] [-overwrite] [-in PATH] [FILE|DIR ...]")
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		return 2
	}
	explicit := ""
	var err error
	if *mapFlag != "" {
		explicit, err = terrainmap.NormalizeMapID(*mapFlag)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 2
		}
	}
	var roots []string
	if *in != "" {
		roots = append(roots, *in)
	}
	roots = append(roots, flags.Args()...)
	if len(roots) == 0 {
		flags.Usage()
		return 2
	}
	inputs, err := collectInputs(roots)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if len(inputs) == 0 {
		fmt.Fprintln(os.Stderr, "no .acmi inputs found")
		return 1
	}
	failed := 0
	for _, input := range inputs {
		info, mapID, err := readAndTransform(input, "", explicit, *overwrite)
		if err != nil {
			failed++
			fmt.Fprintf(os.Stderr, "%s: %v\n", input, err)
			continue
		}
		fmt.Printf("wrote %s: MapId=%s, inserted=%t, rewritten=%d, duplicates_removed=%d, time_qualified=%d\n",
			defaultOutput(input), terrainmap.FormatMapID(mapID), info.inserted, info.rewrote, info.dropped, info.time)
	}
	if failed > 0 {
		fmt.Fprintf(os.Stderr, "%d input(s) failed\n", failed)
		return 1
	}
	return 0
}

func collectInputs(roots []string) ([]string, error) {
	seen := map[string]bool{}
	var files []string
	add := func(path string) {
		abs, err := filepath.Abs(path)
		if err != nil {
			abs = path
		}
		if !seen[abs] {
			seen[abs] = true
			files = append(files, path)
		}
	}
	for _, root := range roots {
		stat, err := os.Stat(root)
		if err != nil {
			return nil, err
		}
		if !stat.IsDir() {
			add(root)
			continue
		}
		err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				return nil
			}
			name := strings.ToLower(entry.Name())
			if strings.HasSuffix(name, ".acmi") && !strings.HasSuffix(name, ".mapid.acmi") {
				add(path)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	sort.Strings(files)
	return files, nil
}
