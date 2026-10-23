package inkjoybridge

import (
	"crypto/x509"
	"encoding/pem"
	"reflect"
	"testing"
)

func TestUpstreamBrokers(t *testing.T) {
	tlsFirst := []string{"mqtts://emqx.inkjoyframe.com:8886", "tcp://emqx.inkjoyframe.com:1883"}
	for _, tc := range []struct {
		in   string
		want []string
	}{
		// Cloud hosts upgrade to MQTTS with the plain fallback, however written.
		{"13.39.148.101:1883", tlsFirst},
		{"13.39.148.101", tlsFirst},
		{"mqtts://emqx.inkjoyframe.com:8886", tlsFirst},
		{"emqx.inkjoyframe.com:1883", tlsFirst},
		{"101.201.79.63:1883", []string{"mqtts://emqx.ink.epaperframe.com:8886", "tcp://emqx.ink.epaperframe.com:1883"}},
		{"8.163.43.11:1883", []string{"mqtts://emqx.advisor.epaperframe.com:8886", "tcp://emqx.advisor.epaperframe.com:1883"}},
		// An explicit plain scheme opts out and is used as written.
		{"tcp://13.39.148.101:1883", []string{"tcp://13.39.148.101:1883"}},
		// Other brokers are used as given.
		{"192.168.1.5:1883", []string{"tcp://192.168.1.5:1883"}},
		{"ssl://broker.example:8883", []string{"mqtts://broker.example:8883"}},
	} {
		if got := upstreamBrokers(tc.in); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("upstreamBrokers(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestEmbeddedInkJoyRootCA(t *testing.T) {
	blk, _ := pem.Decode(inkjoyDeviceRootCAPEM)
	if blk == nil {
		t.Fatal("embedded CA is not PEM")
	}
	cert, err := x509.ParseCertificate(blk.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if cert.Subject.CommonName != "InkJoy Device Root CA" || !cert.IsCA {
		t.Fatalf("embedded CA: CN=%q IsCA=%v", cert.Subject.CommonName, cert.IsCA)
	}
	if upstreamTLSConfig().RootCAs == nil {
		t.Fatal("no root pool")
	}
}
