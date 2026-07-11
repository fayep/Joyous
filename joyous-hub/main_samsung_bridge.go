//go:build samsungbridge

package main

import (
	"context"
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"joyous-hub/bridgehub"
	"joyous-hub/internal/linkmeta"
	"joyous-hub/protocol"
)

func main() {
	hubMQTT := flag.String("hub-mqtt", "tcp://127.0.0.1:11883", "hub joyous MQTT broker")
	bridgeID := flag.String("bridge-id", protocol.KindSamsung, "bridge id announced to hub")
	listenHTTP := flag.String("listen-http", ":18082", "HTTP listen (/samsung frame pull routes)")
	dataDir := flag.String("data-dir", "./data-samsung", "bridge data directory")
	serverAddr := flag.String("server-addr", "", "public HTTP address for Samsung content URLs")
	discoverSubnetsFlag := flag.String("discover-subnets", "", "comma-separated LAN prefixes for MDC fallback sweep")
	flag.Parse()

	if err := os.MkdirAll(*dataDir, 0755); err != nil {
		log.Fatalf("data-dir: %v", err)
	}
	discoverSubnets = parseDiscoverSubnets(*discoverSubnetsFlag)

	log.Printf("samsung-bridge version %s", linkmeta.Version)

	devices := NewDeviceRegistry(*dataDir)
	if err := devices.Load(); err != nil {
		log.Printf("warn: load devices: %v", err)
	}
	colorStore := NewColorStore(*dataDir)
	samsungStore := NewSamsungStore(*dataDir)
	samsungStore.SetColorStore(colorStore)
	samsungBattery := NewSamsungBatteryStore(*dataDir)
	if err := samsungBattery.Load(); err != nil {
		log.Printf("warn: load samsung battery history: %v", err)
	}

	addr := *serverAddr
	if addr == "" {
		addr = localAddr(*listenHTTP)
	}

	hub := &Hub{
		devices:        devices,
		samsungBattery: samsungBattery,
		samsungAliases: loadSamsungFrameAliases(filepath.Join(*dataDir, "samsung")),
		samsung:        samsungStore,
		sendDelivery:   NewSendDeliveryTracker(),
		color:          colorStore,
		serverAddr:     addr,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var hubClient *bridgehub.Client
	hubClient, err := bridgehub.Connect(bridgehub.ClientConfig{
		HubMQTT:  *hubMQTT,
		BridgeID: *bridgeID,
		Kind:     protocol.KindSamsung,
		OnCommand: func(cmd protocol.CmdPayload) {
			handleSamsungBridgeCommand(ctx, hub, hubClient, cmd)
		},
	})
	if err != nil {
		log.Fatalf("hub connect: %v", err)
	}
	defer hubClient.Disconnect()

	mux := http.NewServeMux()
	registerSamsungBridgeHTTP(mux, hub)
	httpServer := &http.Server{Addr: *listenHTTP, Handler: accessLogMiddleware(mux)}
	go func() {
		log.Printf("samsung-bridge HTTP on %s (content at http://%s/samsung/…)", *listenHTTP, addr)
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
			devs := bridgeDevicesFromRegistry(reg, DeviceTypeSamsung)
			_ = client.PublishDevices(devs)
			state, _ := json.Marshal(map[string]any{"devices": devs})
			_ = client.PublishUIState(protocol.UIStatePayload{
				Revision: int(time.Now().Unix()),
				State:    state,
			})
		}
	}
}

func handleSamsungBridgeCommand(ctx context.Context, hub *Hub, client *bridgehub.Client, cmd protocol.CmdPayload) {
	switch cmd.Cmd {
	case protocol.CmdDiscover:
		rec := &bridgeDiscoverRecorder{hub: hub, client: client}
		rec.runDiscover()
	default:
		log.Printf("samsung-bridge: unhandled cmd %q", cmd.Cmd)
	}
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
