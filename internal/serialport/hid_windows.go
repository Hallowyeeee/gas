//go:build windows

package serialport

import (
	"io"
	"strings"

	"golang.org/x/sys/windows"
)

var hidGUID = windows.GUID{Data1: 0x4D1E55B2, Data2: 0xF16F, Data3: 0x11CF, Data4: [8]byte{0x88, 0xCB, 0x00, 0x11, 0x11, 0x00, 0x00, 0x30}}

// HIDPaths 列出 VID/PID 符合的 HID 裝置路徑（match 例如 "vid_0416&pid_5020"）。
func HIDPaths(match string) []string {
	l, err := windows.CM_Get_Device_Interface_List("", &hidGUID, windows.CM_GET_DEVICE_INTERFACE_LIST_PRESENT)
	if err != nil {
		return nil
	}
	var out []string
	for _, p := range l {
		if strings.Contains(strings.ToLower(p), match) {
			out = append(out, p)
		}
	}
	return out
}

type hidDev struct{ h windows.Handle }

func (d *hidDev) Read(b []byte) (int, error) {
	var n uint32
	if err := windows.ReadFile(d.h, b, &n, nil); err != nil {
		return int(n), err
	}
	return int(n), nil
}

func (d *hidDev) Close() error {
	windows.CancelIoEx(d.h, nil)
	return windows.CloseHandle(d.h)
}

// OpenHID 打開 HID 裝置讀資料（共用模式，原廠程式開著也能讀）。每次 Read 回傳一包。
func OpenHID(path string) (io.ReadCloser, error) {
	p, _ := windows.UTF16PtrFromString(path)
	h, err := windows.CreateFile(p, windows.GENERIC_READ|windows.GENERIC_WRITE, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		h, err = windows.CreateFile(p, windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, 0, 0)
		if err != nil {
			return nil, err
		}
	}
	return &hidDev{h}, nil
}
