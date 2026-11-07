//go:build windows

package main

import (
	"context"
	"strings"

	"golang.org/x/sys/windows"
)

var hidGUID = windows.GUID{Data1: 0x4D1E55B2, Data2: 0xF16F, Data3: 0x11CF, Data4: [8]byte{0x88, 0xCB, 0x00, 0x11, 0x11, 0x00, 0x00, 0x30}}

// hidPaths 列出所有 HID 裝置的路徑。
func hidPaths() []string {
	l, err := windows.CM_Get_Device_Interface_List("", &hidGUID, windows.CM_GET_DEVICE_INTERFACE_LIST_PRESENT)
	if err != nil {
		say("  讀不到 HID 清單：%v", err)
		return nil
	}
	return l
}

// readHID 打開一個 HID 裝置，把收到的每一包資料交給 got；ctx 結束時停。
func readHID(ctx context.Context, path string, opened func(), got func([]byte)) error {
	p, _ := windows.UTF16PtrFromString(path)
	h, err := windows.CreateFile(p, windows.GENERIC_READ|windows.GENERIC_WRITE, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		h, err = windows.CreateFile(p, windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, 0, 0)
		if err != nil {
			return err
		}
	}
	opened()
	go func() { <-ctx.Done(); windows.CancelIoEx(h, nil); windows.CloseHandle(h) }()
	buf := make([]byte, 1024)
	for ctx.Err() == nil {
		var n uint32
		if err := windows.ReadFile(h, buf, &n, nil); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		if n > 0 {
			got(append([]byte(nil), buf[:n]...))
		}
	}
	return nil
}

func isHIDBox(path string) bool {
	p := strings.ToLower(path)
	return strings.Contains(p, "vid_0416&pid_5020")
}
