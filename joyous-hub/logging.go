package main

import (
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

type statusResponseWriter struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (w *statusResponseWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusResponseWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := w.ResponseWriter.Write(b)
	w.bytes += n
	return n, err
}

// accessLogMiddleware logs every inbound HTTP request (hub UI, API, frame fetches).
func accessLogMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusResponseWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
		log.Printf("access: %s %s %s %d %dB %s",
			clientAddr(r),
			r.Method,
			r.URL.RequestURI(),
			sw.status,
			sw.bytes,
			time.Since(start).Round(time.Millisecond),
		)
	})
}

func clientAddr(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		return xff
	}
	if r.RemoteAddr != "" {
		return r.RemoteAddr
	}
	return "-"
}

func logOutbound(format string, args ...any) {
	log.Printf("outbound: "+format, args...)
}

type outboundHTTPTransport struct {
	base http.RoundTripper
}

func (t outboundHTTPTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	start := time.Now()
	resp, err := base.RoundTrip(req)
	elapsed := time.Since(start).Round(time.Millisecond)
	if err != nil {
		logOutbound("http %s %s err=%v %s", req.Method, req.URL, err, elapsed)
		return resp, err
	}
	logOutbound("http %s %s %d %s", req.Method, req.URL, resp.StatusCode, elapsed)
	return resp, err
}

func outboundHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout:   timeout,
		Transport: outboundHTTPTransport{},
	}
}

func logFrameSend(deviceID, imageID, kind string, err error) {
	if err != nil {
		log.Printf("frame: send %s %s image=%s err=%v", kind, deviceID, imageID, err)
		return
	}
	log.Printf("frame: send %s %s image=%s ok", kind, deviceID, imageID)
}

func setupFileLogging(dir string) error {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	stdout, err := os.OpenFile(filepath.Join(dir, "stdout.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	stderr, err := os.OpenFile(filepath.Join(dir, "stderr.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		stdout.Close()
		return err
	}
	log.SetOutput(io.MultiWriter(os.Stderr, stderr))
	os.Stdout = stdout
	return nil
}
