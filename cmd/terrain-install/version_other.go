//go:build !windows

package main

import "errors"

func readExecutableFileVersion(string) (string, error) {
	return "", errors.New("Windows executable versions are unavailable on this platform")
}
