package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

const (
	ssdpAddr = "239.255.255.250:1900"
	ssdpMX   = 3
)

// discoverSubnets holds optional LAN prefixes for MDC (TCP :1515) sweep when SSDP is silent.
var discoverSubnets []string

// SSDPDevice is one UPnP/SSDP M-SEARCH response.
type SSDPDevice struct {
	IP                string
	ST                string
	USN               string
	Location          string
	Server            string
	Raw               string
	DisplayCropFormat string
	DisplayWidth      int
	DisplayHeight     int
}

func (d SSDPDevice) DisplayProfile() SamsungDisplayProfile {
	if d.DisplayCropFormat != "" || d.DisplayWidth > 0 || d.DisplayHeight > 0 {
		p := SamsungDisplayProfile{
			CropFormat: d.DisplayCropFormat,
			Width:      d.DisplayWidth,
			Height:     d.DisplayHeight,
		}
		if p.CropFormat == "" {
			p.CropFormat = cropFormatForSize(p.Width, p.Height)
		}
		if p.Width <= 0 || p.Height <= 0 {
			def := defaultSamsungDisplayProfile()
			p.Width, p.Height = def.Width, def.Height
		}
		return p
	}
	return inferSamsungDisplay(d.Server, "")
}

// DisplayName returns a short discovery label: model (when known) plus IP.
func (d SSDPDevice) DisplayName() string {
	model := samsungSSDPModel(d)
	if d.IP != "" {
		return model + " · " + d.IP
	}
	return model
}

func samsungSSDPModel(d SSDPDevice) string {
	combined := ssdpCombined(d)
	if strings.Contains(combined, "em32") {
		return "EM32DX"
	}
	if d.Server != "" && !strings.HasPrefix(strings.ToLower(d.Server), "unspecified") {
		s := d.Server
		if i := strings.LastIndex(s, "/"); i >= 0 {
			s = s[i+1:]
		}
		s = strings.TrimSpace(s)
		lower := strings.ToLower(s)
		if s != "" && lower != "mdc" && lower != "samsung mdc" {
			return s
		}
	}
	return "Samsung"
}

func ssdpCombined(d SSDPDevice) string {
	return strings.ToLower(d.ST + " " + d.USN + " " + d.Server + " " + d.Location + " " + d.Raw)
}

// isExcludedSSDPDevice filters obvious non-frame UPnP devices.
func isExcludedSSDPDevice(d SSDPDevice) bool {
	combined := ssdpCombined(d)
	for _, needle := range []string{
		"hdhomerun", "roku", "lg webos", "webostv", "google cast", "chromecast",
		"amazon", "sonos", "apple tv", "xbox", "playstation", "denon", "yamaha",
	} {
		if strings.Contains(combined, needle) {
			return true
		}
	}
	return false
}

// ClassifyFrameType returns a device type if SSDP headers already identify a frame.
func ClassifyFrameType(d SSDPDevice) (DeviceType, bool) {
	if isExcludedSSDPDevice(d) {
		return "", false
	}
	combined := ssdpCombined(d)
	switch {
	case strings.Contains(combined, "samsung"),
		strings.Contains(combined, "samsung.com"),
		strings.Contains(combined, "epaper"),
		strings.Contains(combined, "e-paper"),
		strings.Contains(combined, "emdx"),
		strings.Contains(combined, "em32"),
		strings.Contains(combined, "mdc"),
		strings.Contains(combined, "vxtplayer"),
		strings.Contains(combined, "com.samsung.ios.epaper"):
		return DeviceTypeSamsung, true
	default:
		return "", false
	}
}

func locationIndicatesSamsung(body string) bool {
	b := strings.ToLower(body)
	for _, needle := range []string{
		"samsung", "epaper", "e-paper", "em32", "emdx", "vxtplayer",
		"com.samsung.ios.epaper", "photo frame", "photoframe",
	} {
		if strings.Contains(b, needle) {
			return true
		}
	}
	return false
}

func fetchSSDPLocation(url string) (string, error) {
	return fetchSSDPLocationCtx(context.Background(), url)
}

func fetchSSDPLocationCtx(ctx context.Context, url string) (string, error) {
	if url == "" {
		return "", io.EOF
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	client := outboundHTTPClient(2 * time.Second)
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", io.EOF
	}
	const maxBody = 64 << 10
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	return string(b), err
}

// identifySamsungFrame applies header, UPnP description, and MDC heuristics.
func identifySamsungFrame(d SSDPDevice) bool {
	return identifySamsungFrameCtx(context.Background(), d)
}

func identifySamsungFrameCtx(ctx context.Context, d SSDPDevice) bool {
	if isExcludedSSDPDevice(d) {
		return false
	}
	if _, ok := ClassifyFrameType(d); ok {
		return true
	}
	if ctx.Err() != nil {
		return false
	}
	if d.Location != "" {
		if body, err := fetchSSDPLocationCtx(ctx, d.Location); err == nil && locationIndicatesSamsung(body) {
			prof := inferSamsungDisplay(d.Server, body)
			d.DisplayCropFormat = prof.CropFormat
			d.DisplayWidth = prof.Width
			d.DisplayHeight = prof.Height
			return true
		}
	}
	if ctx.Err() != nil {
		return false
	}
	return probeMDCBanner(d.IP)
}

func dedupeSSDPByIP(devs []SSDPDevice) []SSDPDevice {
	byIP := make(map[string]SSDPDevice, len(devs))
	for _, d := range devs {
		if prev, ok := byIP[d.IP]; !ok || scoreSSDPDevice(d) > scoreSSDPDevice(prev) {
			byIP[d.IP] = d
		}
	}
	out := make([]SSDPDevice, 0, len(byIP))
	for _, d := range byIP {
		out = append(out, d)
	}
	return out
}

func scoreSSDPDevice(d SSDPDevice) int {
	n := 0
	for _, s := range []string{d.ST, d.USN, d.Server, d.Location} {
		if s != "" {
			n++
		}
	}
	return n
}

// DiscoverPhotoFrames runs SSDP multicast plus optional MDC subnet sweep.
// ssdpSeen is the count of unique responses before frame filtering.
func DiscoverPhotoFrames(timeout time.Duration) (frames []SSDPDevice, ssdpSeen int, err error) {
	if timeout <= 0 {
		timeout = time.Duration(ssdpMX+1) * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()

	var candidates []SSDPDevice
	var mu sync.Mutex
	addCandidates := func(devs []SSDPDevice) {
		mu.Lock()
		candidates = append(candidates, devs...)
		mu.Unlock()
	}

	var wg sync.WaitGroup

	// Native hosts: SSDP multicast is the primary discovery path.
	wg.Add(1)
	go func() {
		defer wg.Done()
		addCandidates(ssdpSearch(ctx, timeout, "ssdp:all"))
	}()

	// Samsung frames often skip SSDP; probe configured LAN prefixes via MDC (TCP :1515).
	if len(discoverSubnets) > 0 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			addCandidates(mdcSubnetSweep(ctx, discoverSubnets, 6*time.Second))
		}()
	}

	wg.Wait()

	candidates = dedupeSSDPByIP(candidates)
	ssdpSeen = len(candidates)

	sem := make(chan struct{}, 8)
	var resolveWG sync.WaitGroup
	var frameMu sync.Mutex
resolveLoop:
	for _, d := range candidates {
		if ctx.Err() != nil {
			break
		}
		if isExcludedSSDPDevice(d) {
			continue
		}
		if _, ok := ClassifyFrameType(d); ok {
			frameMu.Lock()
			frames = append(frames, d)
			frameMu.Unlock()
			continue
		}
		if !needsSlowIdentify(d) {
			continue
		}
		resolveWG.Add(1)
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			resolveWG.Done()
			break resolveLoop
		}
		go func(dev SSDPDevice) {
			defer resolveWG.Done()
			defer func() { <-sem }()
			if identifySamsungFrameCtx(ctx, dev) {
				frameMu.Lock()
				frames = append(frames, dev)
				frameMu.Unlock()
			}
		}(d)
	}
	resolveWG.Wait()
	return dedupeSSDPByIP(frames), ssdpSeen, nil
}

// discoverSSDP fans out SSDP multicast, SSDP unicast sweep, and MDC port scan.
// Samsung frames awake on the LAN often expose MDC (TCP :1515) but not SSDP (UDP :1900).
func discoverSSDP(ctx context.Context, searchTimeout time.Duration, subnets []string, discovered chan<- SSDPDevice, seenCount *atomic.Int32) {
	defer close(discovered)

	seen := make(map[string]struct{})
	var mu sync.Mutex
	emit := func(d SSDPDevice) {
		key := d.IP + "|" + d.ST + "|" + d.USN
		mu.Lock()
		if _, dup := seen[key]; dup {
			mu.Unlock()
			return
		}
		seen[key] = struct{}{}
		mu.Unlock()
		seenCount.Add(1)
		select {
		case discovered <- d:
		case <-ctx.Done():
		}
	}

	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		for _, d := range ssdpSearch(ctx, searchTimeout, "ssdp:all") {
			emit(d)
		}
	}()

	if len(subnets) > 0 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for _, d := range mdcSubnetSweep(ctx, subnets, 4*time.Second) {
				emit(d)
			}
		}()
	} else {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for _, d := range ssdpUnicastSweep(ctx, subnets, 5*time.Second) {
				emit(d)
			}
		}()
	}

	wg.Wait()
}

// resolveSSDP reads UPnP candidates, resolves frame identity, notifies downstream.
func resolveSSDP(ctx context.Context, discovered <-chan SSDPDevice, resolved chan<- SSDPDevice) {
	defer close(resolved)

	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	notify := func(d SSDPDevice) {
		select {
		case resolved <- d:
		case <-ctx.Done():
		}
	}

loop:
	for d := range discovered {
		if ctx.Err() != nil {
			break loop
		}
		if isExcludedSSDPDevice(d) {
			continue
		}
		if _, ok := ClassifyFrameType(d); ok {
			notify(d)
			continue
		}
		if !needsSlowIdentify(d) {
			continue
		}
		wg.Add(1)
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			wg.Done()
			break loop
		}
		go func(dev SSDPDevice) {
			defer wg.Done()
			defer func() { <-sem }()
			if identifySamsungFrameCtx(ctx, dev) {
				notify(dev)
			}
		}(d)
	}
	wg.Wait()
}

func needsSlowIdentify(d SSDPDevice) bool {
	if d.Location == "" {
		return false
	}
	combined := ssdpCombined(d)
	return strings.Contains(combined, "upnp") ||
		strings.Contains(combined, "unspecified") ||
		strings.Contains(combined, "samsung")
}

// collectSSDP drains resolved frames until the channel closes or ctx expires.
func collectSSDP(ctx context.Context, resolved <-chan SSDPDevice) []SSDPDevice {
	seen := make(map[string]struct{})
	var frames []SSDPDevice
	for {
		select {
		case d, ok := <-resolved:
			if !ok {
				return frames
			}
			key := d.IP + "|" + d.USN
			if _, dup := seen[key]; dup {
				continue
			}
			seen[key] = struct{}{}
			frames = append(frames, d)
		case <-ctx.Done():
			return frames
		}
	}
}

func listenSSDPSocket() (*net.UDPConn, error) {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
	if err != nil {
		return nil, err
	}
	if f, err := conn.File(); err == nil {
		fd := int(f.Fd())
		_ = syscall.SetsockoptInt(fd, syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1)
		_ = syscall.SetsockoptInt(fd, syscall.IPPROTO_IP, syscall.IP_MULTICAST_TTL, 4)
		_ = syscall.SetsockoptInt(fd, syscall.IPPROTO_IP, syscall.IP_MULTICAST_LOOP, 1)
		mreq := &syscall.IPMreq{}
		copy(mreq.Multiaddr[:], net.IPv4(239, 255, 255, 250).To4())
		_ = syscall.SetsockoptIPMreq(fd, syscall.IPPROTO_IP, syscall.IP_ADD_MEMBERSHIP, mreq)
		_ = f.Close()
	}
	return conn, nil
}

func ssdpSearch(ctx context.Context, timeout time.Duration, st string) []SSDPDevice {
	addr, err := net.ResolveUDPAddr("udp4", ssdpAddr)
	if err != nil {
		return nil
	}
	conn, err := listenSSDPSocket()
	if err != nil {
		return nil
	}
	defer conn.Close()

	msg := "M-SEARCH * HTTP/1.1\r\n" +
		"HOST: 239.255.255.250:1900\r\n" +
		"MAN: \"ssdp:discover\"\r\n" +
		"MX: " + itoa(ssdpMX) + "\r\n" +
		"ST: " + st + "\r\n\r\n"

	if _, err := conn.WriteToUDP([]byte(msg), addr); err != nil {
		return nil
	}

	buf := make([]byte, 4096)
	seen := make(map[string]SSDPDevice)
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			break
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			break
		}
		readFor := remaining
		if readFor > 250*time.Millisecond {
			readFor = 250 * time.Millisecond
		}
		_ = conn.SetReadDeadline(time.Now().Add(readFor))
		n, remote, err := conn.ReadFromUDP(buf)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			continue
		}
		d := ssdpDeviceFromResponse(remote.IP.String(), string(buf[:n]))
		key := d.IP + "|" + d.ST + "|" + d.USN
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = d
	}
	out := make([]SSDPDevice, 0, len(seen))
	for _, d := range seen {
		out = append(out, d)
	}
	return out
}

func ssdpDeviceFromResponse(ip, text string) SSDPDevice {
	headers := parseSSDPHeaders(text)
	return SSDPDevice{
		IP:       ip,
		ST:       headers["ST"],
		USN:      headers["USN"],
		Location: headers["LOCATION"],
		Server:   headers["SERVER"],
		Raw:      text,
	}
}

func ssdpUnicastProbe(ip string, timeout time.Duration) (*SSDPDevice, bool) {
	conn, err := listenSSDPSocket()
	if err != nil {
		return nil, false
	}
	defer conn.Close()

	msg := "M-SEARCH * HTTP/1.1\r\n" +
		"HOST: 239.255.255.250:1900\r\n" +
		"MAN: \"ssdp:discover\"\r\n" +
		"MX: 1\r\n" +
		"ST: ssdp:all\r\n\r\n"

	addr := &net.UDPAddr{IP: net.ParseIP(ip), Port: 1900}
	if _, err := conn.WriteToUDP([]byte(msg), addr); err != nil {
		return nil, false
	}
	_ = conn.SetReadDeadline(time.Now().Add(timeout))
	buf := make([]byte, 4096)
	n, remote, err := conn.ReadFromUDP(buf)
	if err != nil {
		return nil, false
	}
	d := ssdpDeviceFromResponse(remote.IP.String(), string(buf[:n]))
	return &d, true
}

// mdcSubnetSweep finds Samsung displays by probing TCP :1515 for the MDC banner.
func mdcSubnetSweep(ctx context.Context, prefixes []string, timeout time.Duration) []SSDPDevice {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ips := subnetIPs(prefixes)
	sem := make(chan struct{}, 32)
	var mu sync.Mutex
	var out []SSDPDevice
	var wg sync.WaitGroup

	for _, ip := range ips {
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		go func(ip string) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-sem }()
			if ctx.Err() != nil || !probeMDCBanner(ip) {
				return
			}
			d := SSDPDevice{
				IP:     ip,
				Server: "Samsung MDC",
				USN:    "mdc:" + ip,
			}
			mu.Lock()
			out = append(out, d)
			mu.Unlock()
		}(ip)
	}
	wg.Wait()
	return dedupeSSDPByIP(out)
}

func subnetIPs(prefixes []string) []string {
	seen := make(map[string]struct{})
	var ips []string
	for _, prefix := range prefixes {
		for _, ip := range subnetRange(prefix) {
			if _, dup := seen[ip]; dup {
				continue
			}
			seen[ip] = struct{}{}
			ips = append(ips, ip)
		}
	}
	return ips
}

// subnetRange expands a bare prefix (192.168.50) or CIDR (192.168.50.0/23) into host IPs.
func subnetRange(prefix string) []string {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		return nil
	}
	if strings.Contains(prefix, "/") {
		if ip, network, err := net.ParseCIDR(prefix); err == nil {
			ip = ip.To4()
			if ip != nil {
				return hostsInCIDR(ip, network)
			}
		}
	}
	base := subnetPrefix(prefix)
	if base == "" {
		return nil
	}
	out := make([]string, 0, 254)
	for host := 1; host <= 254; host++ {
		out = append(out, base+"."+itoa(host))
	}
	return out
}

func hostsInCIDR(networkIP net.IP, network *net.IPNet) []string {
	start := ipToUint32(networkIP)
	mask := ipToUint32(net.IP(network.Mask))
	end := start | ^mask
	if end-start <= 1 {
		return nil
	}
	var out []string
	for addr := start + 1; addr < end; addr++ {
		out = append(out, uint32ToIP(addr).String())
	}
	return out
}

func ipToUint32(ip net.IP) uint32 {
	ip = ip.To4()
	return uint32(ip[0])<<24 | uint32(ip[1])<<16 | uint32(ip[2])<<8 | uint32(ip[3])
}

func uint32ToIP(n uint32) net.IP {
	return net.IPv4(byte(n>>24), byte(n>>16), byte(n>>8), byte(n))
}

// ssdpUnicastSweep probes each IP in the given /24 prefixes using a worker pool.
func ssdpUnicastSweep(ctx context.Context, prefixes []string, timeout time.Duration) []SSDPDevice {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ips := make(chan string, 64)
	results := make(chan SSDPDevice, 64)
	var wg sync.WaitGroup

	for range 64 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ip := range ips {
				if ctx.Err() != nil {
					return
				}
				if d, ok := ssdpUnicastProbe(ip, 350*time.Millisecond); ok {
					select {
					case results <- *d:
					case <-ctx.Done():
						return
					}
				}
			}
		}()
	}

	go func() {
		defer close(ips)
		for _, ip := range subnetIPs(prefixes) {
			if ctx.Err() != nil {
				return
			}
			select {
			case ips <- ip:
			case <-ctx.Done():
				return
			}
		}
	}()

	go func() {
		wg.Wait()
		close(results)
	}()

	seen := make(map[string]SSDPDevice)
	for {
		select {
		case d, ok := <-results:
			if !ok {
				return dedupeSSDPValues(seen)
			}
			key := d.IP + "|" + d.ST + "|" + d.USN
			if _, dup := seen[key]; !dup {
				seen[key] = d
			}
		case <-ctx.Done():
			return dedupeSSDPValues(seen)
		}
	}
}

func subnetPrefix(prefix string) string {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		return ""
	}
	if i := strings.Index(prefix, "/"); i >= 0 {
		prefix = prefix[:i]
	}
	base := strings.TrimSuffix(prefix, ".0")
	base = strings.TrimSuffix(base, ".")
	parts := strings.Split(base, ".")
	if len(parts) != 3 {
		return ""
	}
	return base
}

func dedupeSSDPValues(seen map[string]SSDPDevice) []SSDPDevice {
	out := make([]SSDPDevice, 0, len(seen))
	for _, d := range seen {
		out = append(out, d)
	}
	return out
}

func parseDiscoverSubnets(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	var out []string
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func parseSSDPHeaders(text string) map[string]string {
	headers := make(map[string]string)
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if i == 0 {
			continue
		}
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if line == "" {
			break
		}
		if k, v, ok := strings.Cut(line, ":"); ok {
			headers[strings.ToUpper(strings.TrimSpace(k))] = strings.TrimSpace(v)
		}
	}
	return headers
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [12]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
