//go:build !windows

package app

import "errors"

func openFirewall(port int) error { return errors.New("只有 Windows 需要") }
