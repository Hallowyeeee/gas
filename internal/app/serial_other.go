//go:build !windows

package app

import (
	"io"
	"os"
	"path/filepath"
)

// 開發用（Linux/Mac）：不設定鮑率，直接讀裝置檔。
func listSerialPorts() []string {
	var out []string
	for _, p := range []string{"/dev/ttyUSB*", "/dev/ttyACM*", "/dev/tty.usb*"} {
		m, _ := filepath.Glob(p)
		out = append(out, m...)
	}
	return out
}

func openSerial(name string, baud int) (io.ReadCloser, error) { return os.Open(name) }

func listUSBDevices() []string { return nil }
