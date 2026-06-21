package main

import (
	"bytes"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"time"
)

const (
	mdcPort       = 1515
	defaultMDCPin = "250126"
)

// buildContentJSON returns the manifest Samsung mobile deploy expects.
func buildContentJSON(imageURL, fileID string, fileSize int) []byte {
	type content struct {
		ImageURL string `json:"image_url"`
		FileID   string `json:"file_id"`
		FilePath string `json:"file_path"`
		Duration int    `json:"duration"`
		FileSize string `json:"file_size"`
		FileName string `json:"file_name"`
	}
	type schedule struct {
		StartDate string    `json:"start_date"`
		StopDate  string    `json:"stop_date"`
		StartTime string    `json:"start_time"`
		Contents  []content `json:"contents"`
	}
	manifest := struct {
		Schedule    []schedule `json:"schedule"`
		Name        string     `json:"name"`
		Version     int        `json:"version"`
		CreateTime  string     `json:"create_time"`
		ID          string     `json:"id"`
		ProgramID   string     `json:"program_id"`
		ContentType string     `json:"content_type"`
		DeployType  string     `json:"deploy_type"`
	}{
		Schedule: []schedule{{
			StartDate: "1970-01-01",
			StopDate:  "2999-12-31",
			StartTime: "00:00:00",
			Contents: []content{{
				ImageURL: imageURL,
				FileID:   fileID,
				FilePath: fmt.Sprintf("/home/owner/content/Downloads/vxtplayer/epaper/mobile/contents/%s/%s.png", fileID, fileID),
				Duration: 91326,
				FileSize: fmt.Sprintf("%d", fileSize),
				FileName: fileID + ".png",
			}},
		}},
		Name:        "joyous-hub",
		Version:     1,
		CreateTime:  time.Now().Format("2006-01-02 15:04:05"),
		ID:          fileID,
		ProgramID:   "com.samsung.ios.ePaper",
		ContentType: "ImageContent",
		DeployType:  "MOBILE",
	}
	b, _ := json.Marshal(manifest)
	// Samsung mobile deploy parser expects escaped slashes (see FireTime gist).
	return bytes.ReplaceAll(b, []byte("/"), []byte(`\/`))
}

// mdcContentDownloadPacket builds SET_CONTENT_DOWNLOAD (0xC7/0x53) per vgavro/samsung-mdc:
// data = [subcmd=0x53][StrCoded 0x80][urlLen][url...]
func mdcContentDownloadPacket(url string) ([]byte, error) {
	urlBytes := []byte(url)
	if len(urlBytes) > 254 {
		return nil, fmt.Errorf("MDC URL too long (%d bytes)", len(urlBytes))
	}
	const (
		header  = 0xAA
		cmdID   = 0xC7
		devID   = 0x00
		subCmd  = 0x53
		strCode = 0x80 // StrCoded URL field (not 0x00 from older APK notes)
	)
	data := make([]byte, 0, 3+len(urlBytes))
	data = append(data, subCmd, strCode, byte(len(urlBytes)))
	data = append(data, urlBytes...)

	pkt := make([]byte, 0, 5+len(data))
	pkt = append(pkt, header, cmdID, devID, byte(len(data)))
	pkt = append(pkt, data...)
	sum := 0
	for _, b := range pkt[1:] {
		sum += int(b)
	}
	pkt = append(pkt, byte(sum&0xFF))
	return pkt, nil
}

// SendMDCContentDownload optionally WoL-wakes the display, then sends content.json URL via MDC.
func SendMDCContentDownload(ip, contentJSONURL, pin, wolMAC string) error {
	if pin == "" {
		pin = defaultMDCPin
	}
	logOutbound("mdc push start ip=%s url=%s wol=%t", ip, contentJSONURL, wolMAC != "")
	if wolMAC != "" {
		sendWoL(wolMAC)
		time.Sleep(2 * time.Second)
	}
	err := sendMDC(ip, pin, contentJSONURL)
	if err != nil {
		logOutbound("mdc push fail ip=%s url=%s err=%v", ip, contentJSONURL, err)
		return err
	}
	logOutbound("mdc push ok ip=%s url=%s", ip, contentJSONURL)
	return nil
}

func sendMDC(ip, pin, url string) error {
	pkt, err := mdcContentDownloadPacket(url)
	if err != nil {
		return err
	}
	addr := net.JoinHostPort(ip, fmt.Sprintf("%d", mdcPort))
	local := "-"
	d := tcpDialerFor(ip, 10*time.Second)
	if la, ok := d.LocalAddr.(*net.TCPAddr); ok && la.IP != nil {
		local = la.IP.String()
	}
	logOutbound("mdc tcp dial local=%s remote=%s", local, addr)
	conn, err := d.Dial("tcp", addr)
	if err != nil {
		return fmt.Errorf("mdc connect: %w", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))

	banner := make([]byte, 64)
	n, err := conn.Read(banner)
	if err != nil {
		logOutbound("mdc banner read fail ip=%s err=%v", ip, err)
		return fmt.Errorf("mdc banner: %w", err)
	}
	if !bytes.Contains(banner[:n], []byte("MDCSTART")) {
		logOutbound("mdc banner unexpected ip=%s banner=%q", ip, banner[:n])
		return fmt.Errorf("unexpected mdc banner: %q", banner[:n])
	}
	logOutbound("mdc banner ok ip=%s", ip)

	tlsConn := tls.Client(conn, &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12})
	if err := tlsConn.Handshake(); err != nil {
		logOutbound("mdc tls fail ip=%s err=%v", ip, err)
		return fmt.Errorf("mdc tls: %w", err)
	}
	defer tlsConn.Close()
	logOutbound("mdc tls ok ip=%s", ip)

	if _, err := tlsConn.Write([]byte(pin)); err != nil {
		return fmt.Errorf("mdc pin: %w", err)
	}
	auth := make([]byte, 64)
	n, err = tlsConn.Read(auth)
	if err != nil {
		logOutbound("mdc auth read fail ip=%s err=%v", ip, err)
		return fmt.Errorf("mdc auth read: %w", err)
	}
	if !bytes.Contains(auth[:n], []byte("MDCAUTH<<PASS>>")) {
		logOutbound("mdc auth fail ip=%s resp=%q", ip, auth[:n])
		return fmt.Errorf("mdc auth failed: %q", auth[:n])
	}
	logOutbound("mdc auth ok ip=%s", ip)

	if _, err := tlsConn.Write(pkt); err != nil {
		return fmt.Errorf("mdc write: %w", err)
	}
	resp := make([]byte, 64)
	n, readErr := tlsConn.Read(resp)
	if readErr != nil {
		logOutbound("mdc command sent ip=%s url=%s (no response: %v)", ip, url, readErr)
		return nil
	}
	resp = resp[:n]
	if err := parseMDCResponse(resp); err != nil {
		logOutbound("mdc command fail ip=%s url=%s resp=%q err=%v", ip, url, resp, err)
		return err
	}
	logOutbound("mdc command ok ip=%s url=%s resp=%q", ip, url, resp)
	return nil
}

func parseMDCResponse(resp []byte) error {
	if len(resp) < 5 || resp[0] != 0xAA || resp[1] != 0xFF {
		return fmt.Errorf("unexpected mdc response: %q", resp)
	}
	switch resp[4] {
	case 'A':
		return nil
	case 'N':
		return fmt.Errorf("mdc NAK")
	default:
		return fmt.Errorf("unexpected mdc ack byte 0x%02x", resp[4])
	}
}

// probeMDCBanner reports whether the display accepts Samsung MDC on port 1515.
func probeMDCBanner(ip string) bool {
	d := tcpDialerFor(ip, 800*time.Millisecond)
	conn, err := d.Dial("tcp", net.JoinHostPort(ip, fmt.Sprintf("%d", mdcPort)))
	if err != nil {
		return false
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(800 * time.Millisecond))
	buf := make([]byte, 64)
	n, err := conn.Read(buf)
	if err == nil && bytes.Contains(buf[:n], []byte("MDCSTART")) {
		return true
	}
	// Awake Samsung frames accept TCP :1515 even when the banner is slow (matches samsung_serve.py wake probe).
	return true
}

func sendWoL(mac string) {
	mac = strings.ReplaceAll(strings.ReplaceAll(mac, ":", ""), "-", "")
	raw, err := hex.DecodeString(mac)
	if err != nil || len(raw) != 6 {
		logOutbound("wol skip invalid mac=%q", mac)
		return
	}
	packet := bytes.Repeat([]byte{0xff}, 6)
	for i := 0; i < 16; i++ {
		packet = append(packet, raw...)
	}
	conn, err := net.Dial("udp", "255.255.255.255:9")
	if err != nil {
		logOutbound("wol fail mac=%s err=%v", mac, err)
		return
	}
	defer conn.Close()
	if _, err := conn.Write(packet); err != nil {
		logOutbound("wol fail mac=%s err=%v", mac, err)
		return
	}
	logOutbound("wol sent mac=%s", mac)
}
