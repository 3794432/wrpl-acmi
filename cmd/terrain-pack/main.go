package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/maxsupermanhd/wrpl-inspector/v3/internal/terrainbundle"
)

func main() {
	stub := flag.String("stub", "", "stub executable to prefix")
	source := flag.String("source", "", "terrain source directory")
	out := flag.String("out", "", "installer output path")
	flag.Parse()
	if flag.NArg() != 0 || *stub == "" || *source == "" || *out == "" {
		fmt.Fprintln(os.Stderr, "usage: terrain-pack -stub <exe> -source <terrain-dir> -out <installer-exe>")
		os.Exit(2)
	}
	if err := terrainbundle.Pack(*stub, *source, *out); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("wrote %s\n", *out)
}
