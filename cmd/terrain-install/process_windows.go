//go:build windows

package main

import (
	"context"
	"io"
	"os/exec"
	"syscall"
)

func runPowerShellCommand(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	cmd := exec.CommandContext(ctx, "powershell.exe", args...)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return cmd.Run()
}
