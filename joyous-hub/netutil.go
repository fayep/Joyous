package main

import (
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"
)

// requestClientIP returns the client IP for an HTTP request (first X-Forwarded-For hop, else RemoteAddr host).
func requestClientIP(r *http.Request) string {
	if r == nil {
		return ""
	}
	if xff := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); xff != "" {
		if i := strings.Index(xff, ","); i >= 0 {
			xff = strings.TrimSpace(xff[:i])
		}
		return xff
	}
	if r.RemoteAddr == "" {
		return ""
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return strings.TrimSpace(r.RemoteAddr)
	}
	return host
}

// tcpDialerFor returns a dialer that binds outbound TCP to the local interface
// on the same subnet as target. Helps on multi-homed Macs and avoids wrong routes.
func tcpDialerFor(target string, timeout time.Duration) *net.Dialer {
	d := &net.Dialer{Timeout: timeout}
	if ip := outboundLocalIPFor(target); ip != nil {
		d.LocalAddr = &net.TCPAddr{IP: ip}
	}
	return d
}

func outboundLocalIPFor(target string) net.IP {
	targetIP := net.ParseIP(target)
	if targetIP == nil {
		return nil
	}
	targetIP = targetIP.To4()
	if targetIP == nil {
		return nil
	}
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipnet, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip4 := ipnet.IP.To4()
			if ip4 == nil || !ipnet.Contains(targetIP) {
				continue
			}
			return ip4
		}
	}
	return nil
}

func probeNetworkTarget(ip string) error {
	d := tcpDialerFor(ip, 5*time.Second)
	addr := net.JoinHostPort(ip, fmt.Sprintf("%d", mdcPort))
	local := "-"
	if la, ok := d.LocalAddr.(*net.TCPAddr); ok && la.IP != nil {
		local = la.IP.String()
	}
	logOutbound("probe tcp dial local=%s remote=%s", local, addr)
	conn, err := d.Dial("tcp", addr)
	if err != nil {
		return err
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 64)
	n, _ := conn.Read(buf)
	logOutbound("probe ok remote=%s banner=%q", addr, string(buf[:n]))
	return nil
}

// publicHTTPHost returns host:port for URLs fetched by Samsung displays.
// Uses the local LAN IP on the frame's subnet when possible — frames often can't resolve .local names.
func publicHTTPHost(serverAddr, frameIP string) string {
	if serverAddr == "" {
		serverAddr = "localhost:8080"
	}
	host, port, err := net.SplitHostPort(serverAddr)
	if err != nil {
		host = serverAddr
		port = "8080"
	}
	if frameIP != "" {
		if ip := outboundLocalIPFor(frameIP); ip != nil {
			return net.JoinHostPort(ip.String(), port)
		}
	}
	if parsed := net.ParseIP(host); parsed != nil {
		return net.JoinHostPort(parsed.String(), port)
	}
	return net.JoinHostPort(host, port)
}

func samsungMDCContentURL(serverAddr, frameIP, frameID string) string {
	host := publicHTTPHost(serverAddr, frameIP)
	return fmt.Sprintf("http://%s/samsung/%s/content.json", host, frameID)
}

func samsungImageURL(serverAddr, frameIP, frameID string) string {
	host := publicHTTPHost(serverAddr, frameIP)
	return fmt.Sprintf("http://%s/samsung/%s/image", host, frameID)
}
