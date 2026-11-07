//go:build !windows

package serialport

import (
	"errors"
	"io"
)

func HIDPaths(match string) []string { return nil }

func OpenHID(path string) (io.ReadCloser, error) { return nil, errors.New("只支援 Windows") }
