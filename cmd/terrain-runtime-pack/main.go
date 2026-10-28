package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/maxsupermanhd/wrpl-inspector/v3/internal/terrainruntime"
)

func main() {
	stub := flag.String("stub", "", "stub executable to prefix")
	runtime := flag.String("runtime", "", "manifest-verified Python runtime directory")
	out := flag.String("out", "", "bundle output path")
	flag.Parse()
	if flag.NArg() != 0 || *stub == "" || *runtime == "" || *out == "" {
		fmt.Fprintln(os.Stderr, "usage: terrain-runtime-pack -stub <exe> -runtime <runtime-dir> -out <bundle-exe>")
		os.Exit(2)
	}
	if err := terrainruntime.Pack(*stub, *runtime, *out); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("wrote %s\n", *out)
}
