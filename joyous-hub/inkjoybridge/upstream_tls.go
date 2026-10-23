package inkjoybridge

import (
	"crypto/tls"
	"crypto/x509"
	_ "embed"
	"net"
	"strconv"
	"strings"
)

// The cloud's MQTTS brokers (port 8886) present certificates from this CA,
// copied out of the ij_epd v1.3.8 firmware. They ask for no client
// certificate: the frame (and so the bridge) logs in with the same
// username/password as on plain 1883.
//
//go:embed inkjoy_device_root_ca.pem
var inkjoyDeviceRootCAPEM []byte

// cloudTLSPort is the cloud's MQTTS port.
const cloudTLSPort = 8886

// cloudTLSHosts mirrors v1.3.8's "toC" migration table (FUN_4200ee64): each
// legacy cloud address and its MQTTS hostname. The hostnames resolve to the
// same servers.
var cloudTLSHosts = map[string]string{
	"13.39.148.101": "emqx.inkjoyframe.com",
	"101.201.79.63": "emqx.ink.epaperframe.com",
	"8.163.43.11":   "emqx.advisor.epaperframe.com",
}

func cloudTLSHostname(host string) (string, bool) {
	if name, ok := cloudTLSHosts[host]; ok {
		return name, true
	}
	for _, name := range cloudTLSHosts {
		if strings.EqualFold(host, name) {
			return name, true
		}
	}
	return "", false
}

// upstreamBrokers turns an upstream setting (a bare host:port, or a URL
// with a tcp/mqtt or mqtts/ssl/tls scheme) into the broker URLs paho tries
// in order. A cloud host, as an IP or a hostname, is upgraded like the
// firmware does it: MQTTS on <hostname>:8886 first, then plain 1883 as the
// fallback when the TLS handshake fails. An explicit tcp:// or mqtt://
// scheme keeps a plain connection; other hosts are used as given.
func upstreamBrokers(upstream string) []string {
	scheme, hostport := "", strings.TrimSpace(upstream)
	if i := strings.Index(hostport, "://"); i >= 0 {
		scheme, hostport = strings.ToLower(hostport[:i]), hostport[i+3:]
	}
	host, port, err := net.SplitHostPort(hostport)
	if err != nil {
		host, port = hostport, ""
	}
	plain := scheme == "tcp" || scheme == "mqtt"
	if name, ok := cloudTLSHostname(host); ok && !plain {
		return []string{
			"mqtts://" + net.JoinHostPort(name, strconv.Itoa(cloudTLSPort)),
			"tcp://" + net.JoinHostPort(name, "1883"),
		}
	}
	if port == "" {
		port = "1883"
		if isTLSScheme(scheme) {
			port = strconv.Itoa(cloudTLSPort)
		}
	}
	if isTLSScheme(scheme) {
		return []string{"mqtts://" + net.JoinHostPort(host, port)}
	}
	return []string{"tcp://" + net.JoinHostPort(host, port)}
}

func isTLSScheme(s string) bool {
	switch s {
	case "mqtts", "ssl", "tls", "tcps", "mqtt+ssl":
		return true
	}
	return false
}

// upstreamTLSConfig trusts the InkJoy Device Root CA (the cloud's broker
// certificates) on top of the system roots.
func upstreamTLSConfig() *tls.Config {
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	pool.AppendCertsFromPEM(inkjoyDeviceRootCAPEM)
	return &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
}
