//go:build samsungbridge

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"joyous-hub/bridgehub"
	"joyous-hub/internal/linkmeta"
	"joyous-hub/protocol"
)

func main() {
	cfgPath := configPathFromArgs(os.Args)
	if cfgPath == "" {
		var err error
		cfgPath, err = DefaultSamsungConfigPath()
		if err != nil {
			log.Fatalf("config path: %v", err)
		}
	}
	fileCfg, err := LoadSamsungBridgeConfig(cfgPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	if _, err := os.Stat(cfgPath); err == nil {
		log.Printf("config: %s", cfgPath)
	}

	hubMQTT := flag.String("hub-mqtt", fileCfg.HubMQTT, "hub joyous MQTT broker")
	bridgeID := flag.String("bridge-id", fileCfg.BridgeID, "bridge id announced to hub")
	hubHTTP := flag.String("hub-http", fileCfg.HubHTTP, "Joyous hub HTTP base URL")
	listenHTTP := flag.String("listen-http", ":18082", "HTTP listen (legacy direct routes; prefer hub cache)")
	dataDir := flag.String("data-dir", fileCfg.DataDir, "bridge data directory")
	hubDataDir := flag.String("hub-data-dir", fileCfg.HubDataDir, "hub data directory (frame PNG cache)")
	serverAddr := flag.String("server-addr", fileCfg.ServerAddr, "public HTTP address for Samsung content URLs (hub :18080)")
	discoverSubnetsFlag := flag.String("discover-subnets", fileCfg.DiscoverSubnets, "comma-separated LAN prefixes for MDC fallback sweep")
	flag.String("config", cfgPath, "config file")
	flag.Parse()

	if *bridgeID == "" {
		*bridgeID = protocol.KindSamsung
	}
	*hubDataDir = reconcileHubDataDir(*hubDataDir)
	if err := os.MkdirAll(*dataDir, 0755); err != nil {
		log.Fatalf("data-dir: %v", err)
	}
	discoverSubnets = parseDiscoverSubnets(*discoverSubnetsFlag)

	log.Printf("samsung-bridge version %s", linkmeta.Version)

	devices := NewDeviceRegistry(*dataDir)
	if err := devices.Load(); err != nil {
		log.Printf("warn: load devices: %v", err)
	}
	if n, err := devices.ImportSamsungFrom(*hubDataDir); err != nil {
		log.Printf("warn: import hub Samsung devices: %v", err)
	} else if n > 0 {
		log.Printf("imported %d Samsung device(s) from hub data dir %s", n, *hubDataDir)
		if err := devices.Save(); err != nil {
			log.Printf("warn: save imported devices: %v", err)
		}
	}

	pngRoot := strings.TrimSpace(*hubDataDir)
	if pngRoot == "" {
		log.Printf("warn: hub_data_dir unset; Samsung PNGs stay in bridge data_dir (frames may not fetch them)")
		pngRoot = *dataDir
	}
	log.Printf("samsung PNG cache root %s (hub serves GET /samsung/{frameId}/…)", filepath.Join(pngRoot, "samsung"))

	colorStore := NewColorStore(*dataDir)
	samsungStore := NewSamsungStore(pngRoot)
	samsungStore.SetColorStore(colorStore)
	samsungBattery := NewSamsungBatteryStore(*dataDir)
	if err := samsungBattery.Load(); err != nil {
		log.Printf("warn: load samsung battery history: %v", err)
	}

	hubPlayAddr := *serverAddr
	if hubPlayAddr == "" {
		hubPlayAddr = hubHTTPHostPort(*hubHTTP)
	}
	hubPlayIP := resolvedLANIP(hubPlayAddr)

	imageStore, err := NewImageStoreE(*dataDir)
	if err != nil {
		log.Fatalf("image store: %v", err)
	}
	imageStore.SetColorStore(colorStore)
	overlayStore := NewOverlayStore(*dataDir)

	hub := &Hub{
		devices:        devices,
		samsungBattery: samsungBattery,
		samsungAliases: loadSamsungFrameAliases(filepath.Join(pngRoot, "samsung")),
		samsung:        samsungStore,
		sendDelivery:   NewSendDeliveryTracker(),
		color:          colorStore,
		images:         imageStore,
		overlay:        overlayStore,
		serverAddr:     hubPlayAddr,
		hubIP:          hubPlayIP,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	uiHandler := &samsungBridgeHTTPHandler{hub: hub}

	var hubClient *bridgehub.Client
	hubClient, err = bridgehub.Connect(bridgehub.ClientConfig{
		HubMQTT:  *hubMQTT,
		BridgeID: *bridgeID,
		Kind:     protocol.KindSamsung,
		Hello: protocol.HelloPayload{
			Kind:       protocol.KindSamsung,
			ListenHTTP: *listenHTTP,
			// The physical frame calls this back believing it's the paired phone app's own
			// embedded server root — see samsung_content_transfer.go.
			HTTPPaths: []string{"/content-transfer-progress"},
		},
		OnCommand: func(cmd protocol.CmdPayload) {
			handleSamsungBridgeCommand(ctx, hub, hubClient, cmd)
		},
		// Gratuitously republish devices on every (re)connect — including the first — so a hub
		// restart (which drops the in-process broker's retained device state) doesn't leave the
		// Devices tab empty until the next samsungBridgeSyncLoop tick.
		OnReconnect: func(c *bridgehub.Client) { samsungBridgeSyncOnce(c, devices) },
		UIHTTP:      uiHandler,
	})
	if err != nil {
		log.Fatalf("hub connect: %v", err)
	}
	uiHandler.client = hubClient
	defer hubClient.Disconnect()

	mux := http.NewServeMux()
	registerSamsungBridgeHTTP(mux, hub)
	httpServer := &http.Server{Addr: *listenHTTP, Handler: accessLogMiddleware(mux)}
	go func() {
		log.Printf("samsung-bridge HTTP on %s (legacy; frames should pull from hub %s)", *listenHTTP, hubPlayAddr)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("HTTP: %v", err)
		}
	}()

	go samsungBridgeSyncLoop(ctx, hubClient, devices)
	startSamsungOvernightScheduler(ctx, hub)

	<-ctx.Done()
	log.Println("samsung-bridge shutting down...")
	shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	httpServer.Shutdown(shutCtx)
	devices.Save()
	if err := samsungBattery.Save(); err != nil {
		log.Printf("warn: save samsung battery history: %v", err)
	}
}

func registerSamsungBridgeHTTP(mux *http.ServeMux, hub *Hub) {
	mux.HandleFunc("GET /api/samsung", hub.handleSamsungList)
	mux.HandleFunc("POST /api/samsung/poll", hub.handleSamsungPoll)
	mux.HandleFunc("POST /api/samsung/{frameId}/sleep", func(w http.ResponseWriter, r *http.Request) {
		hub.handleSamsungSleep(w, r, r.PathValue("frameId"))
	})
	mux.HandleFunc("POST /api/samsung/{frameId}/wake", func(w http.ResponseWriter, r *http.Request) {
		hub.handleSamsungWake(w, r, r.PathValue("frameId"))
	})
	mux.HandleFunc("POST /api/samsung/{frameId}/push", func(w http.ResponseWriter, r *http.Request) {
		hub.handleSamsungPush(w, r, r.PathValue("frameId"))
	})
	mux.HandleFunc("PUT /api/samsung/{frameId}/config", func(w http.ResponseWriter, r *http.Request) {
		hub.handleSamsungConfigPut(w, r, r.PathValue("frameId"))
	})
	mux.HandleFunc("GET /samsung/{frameId}/content.json", func(w http.ResponseWriter, r *http.Request) {
		hub.handleSamsungContentJSON(w, r, r.PathValue("frameId"))
	})
	mux.HandleFunc("GET /samsung/{frameId}/image", func(w http.ResponseWriter, r *http.Request) {
		hub.handleSamsungImage(w, r, r.PathValue("frameId"))
	})
	mux.HandleFunc("GET /samsung/{frameId}/status", func(w http.ResponseWriter, r *http.Request) {
		hub.handleSamsungStatus(w, r, r.PathValue("frameId"))
	})
}

func samsungBridgeSyncLoop(ctx context.Context, client *bridgehub.Client, reg *DeviceRegistry) {
	tick := time.NewTicker(10 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			samsungBridgeSyncOnce(client, reg)
		}
	}
}

func samsungBridgeSyncOnce(client *bridgehub.Client, reg *DeviceRegistry) {
	devs := bridgeDevicesFromRegistry(reg, DeviceTypeSamsung)
	_ = client.PublishDevices(devs)
	state, _ := json.Marshal(map[string]any{"devices": devs})
	_ = client.PublishUIState(protocol.UIStatePayload{
		Revision: int(time.Now().Unix()),
		State:    state,
	})
}

func handleSamsungBridgeCommand(ctx context.Context, hub *Hub, client *bridgehub.Client, cmd protocol.CmdPayload) {
	switch cmd.Cmd {
	case protocol.CmdDiscover:
		rec := &bridgeDiscoverRecorder{hub: hub, client: client}
		rec.runDiscover()
	case protocol.CmdDeviceTouch:
		var body struct {
			Action string `json:"action"`
		}
		_ = json.Unmarshal(cmd.Body, &body)
		if body.Action == "" {
			body.Action = "hub_contact"
		}
		dev, ok := hub.devices.Get(cmd.DeviceID)
		if !ok || dev.Type != DeviceTypeSamsung || dev.IP == "" {
			log.Printf("samsung-bridge device.touch: device %q not found", cmd.DeviceID)
			return
		}
		switch body.Action {
		case "mdc_sleep":
			hub.devices.NoteSamsungSlept(dev.IP, false)
		case "mdc_deep_sleep":
			hub.devices.NoteSamsungSlept(dev.IP, true)
		default:
			hub.devices.TouchSamsung(dev.IP, body.Action)
			if body.Action == "mdc_wake" || body.Action == "content.json" || body.Action == "png" ||
				body.Action == "mdc_push" || body.Action == "mdc_session" {
				hub.maybeClearSamsungDeepSleepOnFrameContact(SamsungFrameID(dev))
			}
		}
		log.Printf("samsung-bridge device.touch device=%s action=%s", cmd.DeviceID, body.Action)
	case protocol.CmdSendImage:
		var body protocol.SendImageBody
		if err := json.Unmarshal(cmd.Body, &body); err != nil {
			log.Printf("samsung-bridge send.image: bad body: %v", err)
			return
		}
		dev, ok := hub.devices.Get(cmd.DeviceID)
		if !ok || dev.Type != DeviceTypeSamsung {
			log.Printf("samsung-bridge send.image: device %q not found", cmd.DeviceID)
			if body.SendID != "" && client != nil {
				_ = client.PublishSendComplete(protocol.SendCompletePayload{
					SendID:   body.SendID,
					DeviceID: cmd.DeviceID,
					Success:  false,
					Detail:   "device not found on samsung-bridge",
				})
			}
			return
		}
		if err := bridgeDeliverSamsungImage(ctx, hub, client, dev, body); err != nil {
			log.Printf("samsung-bridge send.image: %v", err)
			if body.SendID != "" && client != nil {
				_ = client.PublishSendComplete(protocol.SendCompletePayload{
					SendID:   body.SendID,
					DeviceID: dev.ID,
					Success:  false,
					Detail:   err.Error(),
				})
			}
		}
	default:
		log.Printf("samsung-bridge: unhandled cmd %q", cmd.Cmd)
	}
	if client != nil {
		_ = client.PublishDevices(bridgeDevicesFromRegistry(hub.devices, DeviceTypeSamsung))
	}
}

func bridgeDeliverSamsungImage(ctx context.Context, hub *Hub, client *bridgehub.Client, dev *Device, body protocol.SendImageBody) error {
	pngData, err := bridgeEncodeSamsung(ctx, hub, body, dev)
	if err != nil {
		return fmt.Errorf("encode: %w", err)
	}
	frameID := SamsungFrameID(dev)
	if err := hub.samsung.writePNGLocked(frameID, pngData); err != nil {
		return fmt.Errorf("cache: %w", err)
	}
	etag, _, _ := hub.samsung.PNGInfo(frameID)
	log.Printf("samsung-bridge cache write frame=%s png=%dB etag=%s", frameID, len(pngData), etag)

	if body.SendID != "" && client != nil {
		if err := client.PublishSendComplete(protocol.SendCompletePayload{
			SendID:   body.SendID,
			DeviceID: dev.ID,
			Success:  true,
			Phase:    "bound",
			FrameID:  frameID,
			ETag:     etag,
		}); err != nil {
			log.Printf("samsung-bridge send.complete bound: %v", err)
		}
	}

	imgURL := samsungImageURL(hub.serverAddr, dev.IP, frameID)
	probeCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	err = ProbeSamsungHubURL(probeCtx, imgURL, int64(len(pngData)))
	cancel()
	if err != nil {
		return fmt.Errorf("hub cache probe %s: %w", imgURL, err)
	}
	log.Printf("samsung-bridge hub cache probe ok frame=%s image=%s", frameID, body.ImageID)

	if err := pushSamsungFrameReportingProgress(hub, client, frameID, dev, body.SendID); err != nil {
		return fmt.Errorf("push: %w", err)
	}
	hub.devices.SetLastImage(dev.ID, body.ImageID, body.OverlayToken)
	return nil
}

// pushSamsungFrameReportingProgress relays hub.pushSamsungFrameWithProgress's wake-wait
// attempts to the hub as non-terminal "retrying" SendCompletePayloads, so GET
// /api/send/{sendId} reflects real progress (see send_delivery.go's IncrementRetry) while a
// frame in deep sleep waits — up to mdcManualWakeTimeout (samsung_mdc.go, 5 minutes) — for
// someone to hold its power button for ~3s.
func pushSamsungFrameReportingProgress(hub *Hub, client *bridgehub.Client, frameID string, dev *Device, sendID string) error {
	return hub.pushSamsungFrameWithProgress(frameID, dev, func(phase string, attempt int) {
		if sendID == "" || client == nil {
			return
		}
		detail := "waking frame remotely"
		if phase == wakePhaseManual {
			detail = "waiting for frame to wake"
		}
		if pubErr := client.PublishSendComplete(protocol.SendCompletePayload{
			SendID:   sendID,
			DeviceID: dev.ID,
			Success:  true, // not terminal — see Phase
			Phase:    "retrying",
			Detail:   detail,
		}); pubErr != nil {
			log.Printf("samsung-bridge send.complete retrying: %v", pubErr)
		}
	})
}

type bridgeDiscoverRecorder struct {
	hub    *Hub
	client *bridgehub.Client
}

func (b *bridgeDiscoverRecorder) runDiscover() {
	frames, ssdpSeen, err := DiscoverPhotoFrames(0)
	if err != nil {
		log.Printf("discover: %v", err)
		return
	}
	for _, sd := range frames {
		b.hub.devices.UpsertSamsung(sd)
	}
	_ = b.hub.devices.Save()
	log.Printf("discover: ssdp=%d frames=%d", ssdpSeen, len(frames))
	if b.client != nil {
		_ = b.client.PublishDevices(bridgeDevicesFromRegistry(b.hub.devices, DeviceTypeSamsung))
	}
}
