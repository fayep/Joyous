package main

import (
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

// devicesListPollLogQuiet is how long noisy UI poll endpoints must be idle before the
// next request is logged again (/api/devices every 5s, /api/mqtt/logs every 1s).
const devicesListPollLogQuiet = 30 * time.Second

type quietAccessLogger struct {
	mu     sync.Mutex
	armed  bool
	lastAt time.Time
	quiet  time.Duration
}

func newQuietAccessLogger(quiet time.Duration) *quietAccessLogger {
	return &quietAccessLogger{armed: true, quiet: quiet}
}

// shouldLog reports whether this hit should be logged. The first hit after arming
// logs once; further hits are suppressed until quiet elapses with no hits.
func (q *quietAccessLogger) shouldLog() bool {
	q.mu.Lock()
	defer q.mu.Unlock()

	now := time.Now()
	if !q.lastAt.IsZero() && now.Sub(q.lastAt) >= q.quiet {
		q.armed = true
	}
	q.lastAt = now
	if !q.armed {
		return false
	}
	q.armed = false
	return true
}

var devicesListAccessLog = newQuietAccessLogger(devicesListPollLogQuiet)
var mqttLogsAccessLog = newQuietAccessLogger(devicesListPollLogQuiet)
var samsungListAccessLog = newQuietAccessLogger(devicesListPollLogQuiet)

func isDevicesListPoll(r *http.Request) bool {
	return r.Method == http.MethodGet && r.URL.Path == "/api/devices"
}

func isMQTTLogsPoll(r *http.Request) bool {
	return r.Method == http.MethodGet && r.URL.Path == "/api/mqtt/logs"
}

func isSamsungListPoll(r *http.Request) bool {
	return r.Method == http.MethodGet && r.URL.Path == "/api/samsung"
}

func isImageThumbOrPreview(r *http.Request) bool {
	if r.Method != http.MethodGet {
		return false
	}
	p := r.URL.Path
	return strings.HasPrefix(p, "/images/") &&
		(strings.HasSuffix(p, "/thumb") || strings.HasSuffix(p, "/preview"))
}

func isSamsungFramePNG(r *http.Request) bool {
	if r.Method != http.MethodGet {
		return false
	}
	p := r.URL.Path
	return strings.HasPrefix(p, "/samsung/") && strings.HasSuffix(p, ".png")
}

func shouldSkipAccessLog(r *http.Request, status int) bool {
	if isDevicesListPoll(r) && !devicesListAccessLog.shouldLog() {
		return true
	}
	if isMQTTLogsPoll(r) && !mqttLogsAccessLog.shouldLog() {
		return true
	}
	if isSamsungListPoll(r) && !samsungListAccessLog.shouldLog() {
		return true
	}
	// UI reloads revalidate every cached thumb/png; 304 means nothing changed.
	if status == http.StatusNotModified &&
		(isImageThumbOrPreview(r) || isSamsungFramePNG(r)) {
		return true
	}
	return false
}

// accessLogMiddleware logs every inbound HTTP request (hub UI, API, frame fetches).
func accessLogMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusResponseWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
		if shouldSkipAccessLog(r, sw.status) {
			return
		}
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
