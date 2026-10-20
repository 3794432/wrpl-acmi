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

type inputFile struct {
	path    string
	fromDir bool
}

func run(args []string) int {
	flags := flag.NewFlagSet("acmi-atlas", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	in := flags.String("in", "", "input .acmi file or directory (may also be supplied as positional paths)")
	mapFlag := flags.String("map", "", "explicit canonical map id or WarThunder map id")
	overwrite := flags.Bool("overwrite", false, "atomically replace an existing output")
	flags.Usage = func() {
		fmt.Fprintln(flags.Output(), "usage: acmi-atlas [-map ID] [-overwrite] [-in PATH] [FILE|DIR ...]")
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
		fmt.Fprintln(os.Stderr, "no ungenerated .acmi inputs found")
		return 0
	}

	failed := 0
	for _, input := range inputs {
		if input.fromDir && !*overwrite {
			exists, err := pathExists(defaultOutput(input.path))
			if err != nil {
				failed++
				fmt.Fprintf(os.Stderr, "%s: %v\n", input.path, err)
				continue
			}
			if exists {
				fmt.Printf("skip %s: output exists\n", input.path)
				continue
			}
		}
		info, mapID, err := readAndTransform(input.path, "", explicit, *overwrite)
		if err != nil {
			failed++
			fmt.Fprintf(os.Stderr, "%s: %v\n", input.path, err)
			continue
		}
		fmt.Printf("wrote %s: MapId=%s, T=%d, globals=%d, inserted=%d\n",
			defaultOutput(input.path), terrainmap.FormatAtlasMapID(mapID),
			info.transformed, info.globals, info.inserted)
	}
	if failed > 0 {
		fmt.Fprintf(os.Stderr, "%d input(s) failed\n", failed)
		return 1
	}
	return 0
}

func collectInputs(roots []string) ([]inputFile, error) {
	seen := map[string]int{}
	var files []inputFile
	add := func(path string, fromDir bool) {
		abs, err := filepath.Abs(path)
		if err != nil {
			abs = path
		}
		key := strings.ToLower(filepath.Clean(abs))
		if i, ok := seen[key]; ok {
			files[i].fromDir = files[i].fromDir && fromDir
			return
		}
		seen[key] = len(files)
		files = append(files, inputFile{path: path, fromDir: fromDir})
	}

	for _, root := range roots {
		stat, err := os.Stat(root)
		if err != nil {
			return nil, err
		}
		if !stat.IsDir() {
			add(root, false)
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
			if strings.HasSuffix(name, ".acmi") && !strings.HasSuffix(name, ".atlas.acmi") {
				add(path, true)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].path < files[j].path })
	return files, nil
}

func defaultOutput(input string) string {
	if strings.HasSuffix(strings.ToLower(input), ".atlas.acmi") {
		return input
	}
	return strings.TrimSuffix(input, filepath.Ext(input)) + ".atlas.acmi"
}

func pathExists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}
