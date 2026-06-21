package main

import (
	"bytes"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strings"
	"time"
)

const (
	mdcPort              = 1515
	defaultMDCPin        = "250126"
	mdcCmdBattery        = 0x1B
	mdcSubCmdBattery     = 0x73
	mdcConnectTimeout    = 10 * time.Second
	mdcCommandReadTimeout = 5 * time.Second
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
	s, err := openMDCSession(ip, pin, mdcConnectTimeout)
	if err != nil {
		return err
	}
	defer s.Close()
	if err := s.transact(pkt); err != nil {
		logOutbound("mdc command sent ip=%s url=%s (no response: %v)", ip, url, err)
		return nil
	}
	s.setDeadline(mdcCommandReadTimeout)
	resp, err := s.readMDCPacket()
	if err != nil {
		logOutbound("mdc command sent ip=%s url=%s (no response: %v)", ip, url, err)
		return nil
	}
	if err := parseMDCResponse(resp); err != nil {
		logOutbound("mdc command fail ip=%s url=%s resp=%q err=%v", ip, url, resp, err)
		return err
	}
	logOutbound("mdc command ok ip=%s url=%s resp=%q", ip, url, resp)
	return nil
}

// mdcSubCommandQueryPacket builds a GET sub-command packet (cmd/subcmd only, no payload).
func mdcSubCommandQueryPacket(cmdID, subCmd byte) []byte {
	pkt := []byte{0xAA, cmdID, 0x00, 0x01, subCmd}
	sum := 0
	for i := 1; i < len(pkt); i++ {
		sum += int(pkt[i])
	}
	return append(pkt, byte(sum&0xFF))
}

type mdcSession struct {
	conn net.Conn
	tls  *tls.Conn
	ip   string
}

// MDCBatteryResult holds MDC session and optional battery telemetry.
type MDCBatteryResult struct {
	SessionOK   bool
	Percent     int
	PowerSource string
}

func (s *mdcSession) setDeadline(d time.Duration) {
	dl := time.Now().Add(d)
	if s.conn != nil {
		_ = s.conn.SetDeadline(dl)
	}
	if s.tls != nil {
		_ = s.tls.SetDeadline(dl)
	}
}

func openMDCSession(ip, pin string, timeout time.Duration) (*mdcSession, error) {
	if pin == "" {
		pin = defaultMDCPin
	}
	addr := net.JoinHostPort(ip, fmt.Sprintf("%d", mdcPort))
	local := "-"
	d := tcpDialerFor(ip, timeout)
	if la, ok := d.LocalAddr.(*net.TCPAddr); ok && la.IP != nil {
		local = la.IP.String()
	}
	logOutbound("mdc tcp dial local=%s remote=%s", local, addr)
	conn, err := d.Dial("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("mdc connect: %w", err)
	}
	s := &mdcSession{conn: conn, ip: ip}
	s.setDeadline(timeout)

	banner := make([]byte, 64)
	n, err := conn.Read(banner)
	if err != nil {
		conn.Close()
		logOutbound("mdc banner read fail ip=%s err=%v", ip, err)
		return nil, fmt.Errorf("mdc banner: %w", err)
	}
	if !bytes.Contains(banner[:n], []byte("MDCSTART")) {
		conn.Close()
		logOutbound("mdc banner unexpected ip=%s banner=%q", ip, banner[:n])
		return nil, fmt.Errorf("unexpected mdc banner: %q", banner[:n])
	}
	logOutbound("mdc banner ok ip=%s", ip)

	tlsConn := tls.Client(conn, &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12})
	if err := tlsConn.Handshake(); err != nil {
		conn.Close()
		logOutbound("mdc tls fail ip=%s err=%v", ip, err)
		return nil, fmt.Errorf("mdc tls: %w", err)
	}
	s.tls = tlsConn
	logOutbound("mdc tls ok ip=%s", ip)

	s.setDeadline(timeout)
	if _, err := tlsConn.Write([]byte(pin)); err != nil {
		s.Close()
		return nil, fmt.Errorf("mdc pin: %w", err)
	}
	auth := make([]byte, 64)
	n, err = tlsConn.Read(auth)
	if err != nil {
		s.Close()
		logOutbound("mdc auth read fail ip=%s err=%v", ip, err)
		return nil, fmt.Errorf("mdc auth read: %w", err)
	}
	if !bytes.Contains(auth[:n], []byte("MDCAUTH<<PASS>>")) {
		s.Close()
		logOutbound("mdc auth fail ip=%s resp=%q", ip, auth[:n])
		return nil, fmt.Errorf("mdc auth failed: %q", auth[:n])
	}
	logOutbound("mdc auth ok ip=%s", ip)
	return s, nil
}

func (s *mdcSession) Close() error {
	if s.tls != nil {
		_ = s.tls.Close()
	}
	if s.conn != nil {
		return s.conn.Close()
	}
	return nil
}

func (s *mdcSession) transact(pkt []byte) error {
	if _, err := s.tls.Write(pkt); err != nil {
		return fmt.Errorf("mdc write: %w", err)
	}
	return nil
}

func (s *mdcSession) readMDCPacket() ([]byte, error) {
	hdr := make([]byte, 4)
	if _, err := io.ReadFull(s.tls, hdr); err != nil {
		return nil, err
	}
	if hdr[0] != 0xAA {
		return nil, fmt.Errorf("unexpected mdc response start 0x%02x", hdr[0])
	}
	dataLen := int(hdr[3])
	if dataLen <= 0 || dataLen > 250 {
		return nil, fmt.Errorf("unexpected mdc response len %d", dataLen)
	}
	rest := make([]byte, dataLen+1) // body + checksum
	if _, err := io.ReadFull(s.tls, rest); err != nil {
		return nil, err
	}
	return append(hdr, rest...), nil
}

// QueryMDCBatteryLevel opens MDC and queries battery (0x1B/0x73).
// SessionOK is true when TLS+PIN auth succeeds, even if the battery command times out.
func QueryMDCBatteryLevel(ip, pin string) (MDCBatteryResult, error) {
	result := MDCBatteryResult{}
	s, err := openMDCSession(ip, pin, mdcConnectTimeout)
	if err != nil {
		return result, err
	}
	defer s.Close()
	result.SessionOK = true

	pkt := mdcSubCommandQueryPacket(mdcCmdBattery, mdcSubCmdBattery)
	logOutbound("mdc battery query ip=%s pkt=% x", ip, pkt)
	if err := s.transact(pkt); err != nil {
		return result, err
	}
	s.setDeadline(mdcCommandReadTimeout)
	resp, err := s.readMDCPacket()
	if err != nil {
		logOutbound("mdc battery read fail ip=%s err=%v", ip, err)
		return result, fmt.Errorf("mdc battery read: %w", err)
	}
	pct, src, err := parseMDCBatteryResponse(resp)
	if err != nil {
		logOutbound("mdc battery parse fail ip=%s resp=% x err=%v", ip, resp, err)
		return result, err
	}
	result.Percent = pct
	result.PowerSource = src
	logOutbound("mdc battery ok ip=%s pct=%d src=%s", ip, pct, src)
	return result, nil
}

func parseMDCBatteryResponse(resp []byte) (int, string, error) {
	if len(resp) < 13 {
		return 0, "", fmt.Errorf("mdc battery response too short: % x", resp)
	}
	if resp[0] != 0xAA || resp[1] != 0xFF {
		return 0, "", fmt.Errorf("unexpected mdc battery header: % x", resp)
	}
	switch resp[4] {
	case 'A':
	case 'N':
		return 0, "", fmt.Errorf("mdc battery NAK")
	default:
		return 0, "", fmt.Errorf("mdc battery ack 0x%02x", resp[4])
	}
	if resp[5] != mdcCmdBattery || resp[6] != mdcSubCmdBattery {
		return 0, "", fmt.Errorf("mdc battery cmd mismatch: % x", resp[5:7])
	}
	payload := resp[7 : len(resp)-1]
	if len(payload) < 6 {
		return 0, "", fmt.Errorf("mdc battery payload too short: % x", payload)
	}
	pct := int(payload[3])
	if pct < 0 {
		pct = 0
	} else if pct > 100 {
		pct = 100
	}
	return pct, mdcPowerSourceName(payload[5]), nil
}

func mdcPowerSourceName(b byte) string {
	switch b {
	case 1:
		return "ac"
	case 2:
		return "usb"
	case 3:
		return "wireless"
	default:
		return ""
	}
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
