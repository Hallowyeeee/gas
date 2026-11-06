//go:build windows

package main

import "golang.org/x/sys/windows"

func initConsole() {
	windows.SetConsoleOutputCP(65001)
	windows.SetConsoleCP(65001)
}
