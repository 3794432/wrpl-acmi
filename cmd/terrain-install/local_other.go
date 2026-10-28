//go:build !windows

package main

import (
	"context"
	"errors"
	"io"
)

func pauseOnDoubleClick() {}

func runLocalPython(context.Context, string, []string, io.Writer, io.Writer) error {
	return errors.New("local terrain conversion requires Windows x64")
}
func acquireConversionLock(string) (func(), error) {
	return nil, errors.New("local terrain conversion requires Windows x64")
}
