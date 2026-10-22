//go:build !windows

package main

import (
	"context"
	"errors"
	"io"
)

func runPowerShellCommand(context.Context, []string, io.Writer, io.Writer) error {
	return errors.New("terrain installation requires Windows PowerShell")
}
