package app

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"strings"
	"time"
)

// 店內自動找主電腦：分機在店裡網路廣播「主電腦在哪」，主電腦回答自己的身分碼和埠號。
// 用 UDP，埠號 = 網頁埠號 + 1（預設 8081）。

const discoverAsk = "GAS-FIND-1"

func discoveryPort(webPort int) int { return webPort + 1 }

type discoverReply struct {
	ID   string `json:"id"`
	Port int    `json:"port"`
	Name string `json:"name,omitempty"`
}

// ServeDiscovery 主電腦：一直聽分機來問。
func (a *App) ServeDiscovery(ctx context.Context) {
	if a.Port == 0 {
		return
	}
	pc, err := net.ListenPacket("udp4", fmt.Sprintf(":%d", discoveryPort(a.Port)))
	if err != nil {
		log.Println("店內自動找主電腦的功能沒開成：", err)
		return
	}
	go func() { <-ctx.Done(); pc.Close() }()
	reply, _ := json.Marshal(discoverReply{ID: a.serverID(), Port: a.Port})
	buf := make([]byte, 256)
	for {
		n, from, err := pc.ReadFrom(buf)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			continue
		}
		if strings.TrimSpace(string(buf[:n])) == discoverAsk {
			pc.WriteTo(reply, from)
		}
	}
}

// broadcastTargets 店裡網路的廣播位址（每張網卡一個，再加 255.255.255.255）。
func broadcastTargets(port int) []string {
	out := []string{fmt.Sprintf("255.255.255.255:%d", port)}
	ifs, _ := net.Interfaces()
	for _, ifc := range ifs {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagBroadcast == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, ad := range addrs {
			ipn, ok := ad.(*net.IPNet)
			if !ok || ipn.IP.To4() == nil || !ipn.IP.IsPrivate() {
				continue
			}
			ip, mask := ipn.IP.To4(), ipn.Mask
			if len(mask) == 16 {
				mask = mask[12:]
			}
			b := make(net.IP, 4)
			for i := range b {
				b[i] = ip[i] | ^mask[i]
			}
			out = append(out, fmt.Sprintf("%s:%d", b, port))
		}
	}
	return out
}

type found struct {
	Base string // http://192.168.1.10:8080
	ID   string
}

// discover 分機：廣播問一次，等 wait 時間收回答。
func discover(ctx context.Context, targets []string, wait time.Duration) []found {
	pc, err := net.ListenPacket("udp4", ":0")
	if err != nil {
		return nil
	}
	defer pc.Close()
	for _, t := range targets {
		if ua, err := net.ResolveUDPAddr("udp4", t); err == nil {
			pc.WriteTo([]byte(discoverAsk), ua)
		}
	}
	deadline := time.Now().Add(wait)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	pc.SetReadDeadline(deadline)
	var out []found
	seen := map[string]bool{}
	buf := make([]byte, 512)
	for {
		n, from, err := pc.ReadFrom(buf)
		if err != nil {
			return out
		}
		var r discoverReply
		ua, ok := from.(*net.UDPAddr)
		if !ok || json.Unmarshal(buf[:n], &r) != nil || r.ID == "" || r.Port == 0 {
			continue
		}
		base := fmt.Sprintf("http://%s:%d", ua.IP, r.Port)
		if !seen[base] {
			seen[base] = true
			out = append(out, found{base, r.ID})
		}
	}
}
