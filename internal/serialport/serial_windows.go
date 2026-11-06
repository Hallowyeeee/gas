//go:build windows

// Package serialport 讀 COM 埠（來電顯示盒用）。
package serialport

import (
	"context"
	"io"
	"os/exec"
	"sort"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
	"unsafe"
)

// Ports 列出電腦上所有 COM 埠。
func Ports() []string {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, `HARDWARE\DEVICEMAP\SERIALCOMM`, registry.QUERY_VALUE)
	if err != nil {
		return nil
	}
	defer k.Close()
	names, _ := k.ReadValueNames(0)
	var out []string
	for _, n := range names {
		if v, _, err := k.GetStringValue(n); err == nil && v != "" {
			out = append(out, v)
		}
	}
	sort.Strings(out)
	return out
}

type winPort struct{ h windows.Handle }

func (p *winPort) Read(b []byte) (int, error) {
	var n uint32
	if err := windows.ReadFile(p.h, b, &n, nil); err != nil {
		return int(n), err
	}
	return int(n), nil
}

func (p *winPort) Close() error { return windows.CloseHandle(p.h) }

// Open 開 COM 埠（8N1），每次讀最多等半秒。
func Open(name string, baud int) (io.ReadCloser, error) {
	path, _ := windows.UTF16PtrFromString(`\\.\` + name)
	h, err := windows.CreateFile(path, windows.GENERIC_READ|windows.GENERIC_WRITE, 0, nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		return nil, err
	}
	var dcb windows.DCB
	dcb.DCBlength = uint32(unsafe.Sizeof(dcb))
	if err := windows.GetCommState(h, &dcb); err != nil {
		windows.CloseHandle(h)
		return nil, err
	}
	dcb.BaudRate = uint32(baud)
	dcb.ByteSize = 8
	dcb.Parity = 0
	dcb.StopBits = 0
	dcb.Flags = 0x0001 | 0x0010 | 0x1000 // fBinary、DTR 開、RTS 開（有些盒子靠這兩條線供電）
	if err := windows.SetCommState(h, &dcb); err != nil {
		windows.CloseHandle(h)
		return nil, err
	}
	to := windows.CommTimeouts{ReadIntervalTimeout: 0xFFFFFFFF, ReadTotalTimeoutMultiplier: 0xFFFFFFFF, ReadTotalTimeoutConstant: 500}
	if err := windows.SetCommTimeouts(h, &to); err != nil {
		windows.CloseHandle(h)
		return nil, err
	}
	windows.PurgeComm(h, 0x0008|0x0002) // 清掉舊資料
	return &winPort{h}, nil
}

// Devices 列出 COM 埠、USB、人性化介面裝置，給偵測畫面看。
func Devices() []string {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	ps := `[Console]::OutputEncoding=[Text.Encoding]::UTF8; Get-PnpDevice -PresentOnly | Where-Object { $_.Class -in 'Ports','HIDClass','USB','Modem','Media' } | ForEach-Object { $_.Class + ' | ' + $_.FriendlyName + ' | ' + $_.InstanceId }`
	cmd := exec.CommandContext(ctx, "powershell", "-NoProfile", "-NonInteractive", "-Command", ps)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
	out, err := cmd.Output()
	if err != nil {
		return []string{"讀不到裝置清單：" + err.Error()}
	}
	var list []string
	for _, l := range strings.Split(strings.ReplaceAll(string(out), "\r", ""), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			list = append(list, l)
		}
	}
	return list
}
