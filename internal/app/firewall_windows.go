//go:build windows

package app

import (
	"fmt"
	"os/exec"
)

// openFirewall 加一條防火牆規則：只讓同一個網路（店裡）的電腦連 gas 的埠號。
// 會跳出 Windows「是否允許變更」的視窗，要按「是」。
func openFirewall(port int) error {
	args := fmt.Sprintf(`/c netsh advfirewall firewall delete rule name=GasShop & `+
		`netsh advfirewall firewall add rule name=GasShop dir=in action=allow protocol=TCP localport=%d remoteip=localsubnet profile=any`, port)
	cmd := exec.Command("powershell", "-NoProfile", "-WindowStyle", "Hidden", "-Command",
		"Start-Process cmd -Verb RunAs -WindowStyle Hidden -Wait -ArgumentList "+psQuote(args))
	hideWindow(cmd)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%v %s（是不是按了「否」？）", err, out)
	}
	return nil
}

func psQuote(s string) string {
	q := "'"
	for _, c := range s {
		if c == '\'' {
			q += "''"
		} else {
			q += string(c)
		}
	}
	return q + "'"
}
