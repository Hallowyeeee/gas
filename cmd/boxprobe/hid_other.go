//go:build !windows

package main

import "context"

func hidPaths() []string { return nil }
func readHID(ctx context.Context, path string, opened func(), got func([]byte)) error {
	return nil
}
func isHIDBox(path string) bool { return false }
