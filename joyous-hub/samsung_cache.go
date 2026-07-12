package main

import (
	"context"
	"fmt"
	"net/http"
	"strings"
)

const samsungCacheResponseHeader = "X-Joyous-Samsung-Cache"

func setSamsungCacheResponseHeaders(w http.ResponseWriter) {
	w.Header().Set(samsungCacheResponseHeader, "1")
}

// isSamsungFramePullProxyPath reports paths that must be served from the hub
// disk cache, never tunneled to the bridge over MQTT.
// Examples: {frameId}/content.json, {frameId}/image, {frameId}/status, {frameId}.png
func isSamsungFramePullProxyPath(subPath string) bool {
	subPath = strings.Trim(subPath, "/")
	if subPath == "" || strings.HasPrefix(subPath, "api/") {
		return false
	}
	parts := strings.Split(subPath, "/")
	switch len(parts) {
	case 1:
		file := parts[0]
		if strings.HasSuffix(file, ".png") || strings.HasSuffix(file, ".lock") {
			id := strings.TrimSuffix(strings.TrimSuffix(file, ".png"), ".lock")
			return validFrameID(id)
		}
		return false
	case 2:
		frameID, leaf := parts[0], parts[1]
		if !validFrameID(frameID) {
			return false
		}
		switch leaf {
		case "content.json", "image", "status":
			return true
		default:
			return false
		}
	default:
		return false
	}
}

// VerifySamsungCacheServing checks the hub samsung frame-pull HTTP handlers are registered.
// A cache miss (404 + X-Joyous-Samsung-Cache) is success; 502 means the bridge proxy owns the route.
func VerifySamsungCacheServing(ctx context.Context, hubBaseURL string) error {
	base := strings.TrimRight(strings.TrimSpace(hubBaseURL), "/")
	if base == "" {
		return fmt.Errorf("hub base URL required")
	}
	url := base + "/samsung/__startup_probe__/image"
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.Header.Get(samsungCacheResponseHeader) != "" {
		return nil
	}
	if resp.StatusCode == http.StatusBadGateway {
		return fmt.Errorf("GET /samsung/{{frameId}}/image is not served from hub disk (bridge MQTT proxy?)")
	}
	// HEAD may not be registered; try GET
	if resp.StatusCode == http.StatusMethodNotAllowed || resp.StatusCode == http.StatusNotFound {
		req, err = http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return err
		}
		resp2, err := http.DefaultClient.Do(req)
		if err != nil {
			return err
		}
		defer resp2.Body.Close()
		if resp2.Header.Get(samsungCacheResponseHeader) != "" {
			return nil
		}
		if resp2.StatusCode == http.StatusBadGateway {
			return fmt.Errorf("GET /samsung/{{frameId}}/image is not served from hub disk (bridge MQTT proxy?)")
		}
		return fmt.Errorf("samsung cache handler not active (HTTP %d)", resp2.StatusCode)
	}
	return fmt.Errorf("samsung cache handler not active (HTTP %d)", resp.StatusCode)
}

// ProbeSamsungHubURL checks that the hub can serve a frame PNG before MDC push.
func ProbeSamsungHubURL(ctx context.Context, url string, wantBytes int64) error {
	return probeHubURLWithHeader(ctx, url, wantBytes, samsungCacheResponseHeader)
}
