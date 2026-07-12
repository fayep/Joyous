//go:build !inkjoybridge && !samsungbridge && !nixplaybridge

package main

import (
	"context"
	"flag"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	mochi "github.com/mochi-mqtt/server/v2"
	"github.com/mochi-mqtt/server/v2/hooks/auth"
	"github.com/mochi-mqtt/server/v2/listeners"

	"joyous-hub/bridgehub"
	"joyous-hub/internal/linkmeta"
	"joyous-hub/protocol"
)

func main() {
	cfgPath := configPathFromArgs(os.Args)
	if cfgPath == "" {
		var err error
		cfgPath, err = DefaultConfigPath()
		if err != nil {
			log.Fatalf("config path: %v", err)
		}
	}
	fileCfg, err := LoadHubConfig(cfgPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	if _, err := os.Stat(cfgPath); err == nil {
		log.Printf("config: %s", cfgPath)
	}

	mqttAddr := flag.String("listen-mqtt", fileCfg.ListenMQTT, "Joyous bridge MQTT broker address")
	httpAddr := flag.String("listen-http", fileCfg.ListenHTTP, "HTTP server address")
	dataDir := flag.String("data-dir", fileCfg.DataDir, "data directory for devices.json and images/")
	serverAddr := flag.String("server-addr", fileCfg.ServerAddr, "public HTTP address for image URLs (auto-detect if empty)")
	discoverSubnetsFlag := flag.String("discover-subnets", fileCfg.DiscoverSubnets, "comma-separated LAN prefixes for Samsung MDC fallback sweep")
	probeNetworkFlag := flag.String("probe-network", "", "test TCP connectivity to IP:1515 and exit")
	logDirFlag := flag.String("log-dir", fileCfg.LogDir, "append hub logs to stdout.log and stderr.log in this directory")
	flag.String("config", cfgPath, "config file")
	flag.Parse()

	if dir := strings.TrimSpace(*logDirFlag); dir != "" {
		if err := setupFileLogging(dir); err != nil {
			log.Fatalf("log-dir: %v", err)
		}
	}

	if ip := strings.TrimSpace(*probeNetworkFlag); ip != "" {
		if err := probeNetworkTarget(ip); err != nil {
			log.Fatalf("probe-network %s: %v", ip, err)
		}
		log.Printf("probe-network ok: %s", ip)
		return
	}

	discoverSubnets = parseDiscoverSubnets(*discoverSubnetsFlag)
	if len(discoverSubnets) > 0 {
		log.Printf("discovery subnets: %v", discoverSubnets)
	}
	log.Printf("joyous-hub version %s", linkmeta.Version)

	if err := os.MkdirAll(*dataDir, 0755); err != nil {
		log.Fatalf("data-dir: %v", err)
	}

	devices := NewDeviceRegistry(*dataDir)
	if err := devices.Load(); err != nil {
		log.Printf("warn: load devices: %v", err)
	}
	imageStore, err := NewImageStoreE(*dataDir)
	if err != nil {
		log.Fatalf("image store: %v", err)
	}
	colorStore := NewColorStore(*dataDir)
	imageStore.SetColorStore(colorStore)
	displayPreview := NewDisplayPreviewStore(*dataDir)
	inkjoyCache := NewInkJoyCache(*dataDir)
	displayPreview.RestoreFromDisk(devices)
	samsungStore := NewSamsungStore(*dataDir)
	samsungStore.SetColorStore(colorStore)
	samsungBattery := NewSamsungBatteryStore(*dataDir)
	if err := samsungBattery.Load(); err != nil {
		log.Printf("warn: load samsung battery history: %v", err)
	}

	// Joyous MQTT broker — bridges connect here; frames connect to vendor bridges.
	broker := mochi.New(&mochi.Options{InlineClient: true})
	_ = broker.AddHook(new(auth.AllowHook), nil)

	tcpListener := listeners.NewTCP(listeners.Config{
		ID:      "tcp",
		Address: *mqttAddr,
	})
	if err := broker.AddListener(tcpListener); err != nil {
		log.Fatalf("broker listener: %v", err)
	}

	bridgeCoord := bridgehub.NewCoordinator(broker, devices)
	joyousMQTTLog := NewMQTTLogBuffer(20)
	_ = broker.AddHook(&bridgeHook{coord: bridgeCoord, log: joyousMQTTLog}, nil)

	sendDelivery := NewSendDeliveryTracker()
	bridgeCoord.SetSendCompleteHandler(func(body protocol.SendCompletePayload) {
		if sendDelivery == nil || body.SendID == "" {
			return
		}
		switch body.Phase {
		case "bound", "prepared":
			if body.FrameID != "" {
				sendDelivery.BindSamsung(body.SendID, body.FrameID, body.ETag)
				log.Printf("send bound send_id=%s device=%s frame=%s", body.SendID, body.DeviceID, body.FrameID)
			}
		case "downloading":
			sendDelivery.MarkInkJoyDownloading(body.SendID)
			log.Printf("send downloading send_id=%s device=%s", body.SendID, body.DeviceID)
		default:
			sendDelivery.CompleteSend(body.SendID, body.Success)
			if body.Success {
				log.Printf("send delivered send_id=%s device=%s", body.SendID, body.DeviceID)
			} else if body.Detail != "" {
				log.Printf("send failed send_id=%s device=%s: %s", body.SendID, body.DeviceID, body.Detail)
			}
		}
	})
	addr := *serverAddr
	if addr == "" {
		addr = localAddr(*httpAddr)
	}
	hubIP := resolvedLANIP(addr)
	hub := &Hub{
		devices:        devices,
		samsungBattery: samsungBattery,
		samsungAliases: loadSamsungFrameAliases(filepath.Join(*dataDir, "samsung")),
		images:         imageStore,
		displayPreview: displayPreview,
		inkjoy:         inkjoyCache,
		samsung:        samsungStore,
		sendDelivery:   sendDelivery,
		overlay:        NewOverlayStore(*dataDir),
		color:          colorStore,
		scheduledSends: NewScheduledSendStore(*dataDir),
		publisher:      &bridgeCoordinatorPublisher{coord: bridgeCoord},
		bridgeCoord:    bridgeCoord,
		joyousMQTTLog:  joyousMQTTLog,
		serverAddr:     addr,
		hubIP:          hubIP,
	}
	hub.migrateSamsungFramesOnStartup()

	mux := http.NewServeMux()
	registerRoutes(mux, hub)
	registerInkJoyCacheRoutes(mux, inkjoyCache)
	registerBridgeRoutes(mux, hub)

	log.Printf("InkJoy frame .bin cache at %s (GET/HEAD /inkjoy/{mac}/{file}.bin)", filepath.Join(*dataDir, "inkjoy"))
	log.Printf("Samsung frame cache at %s (GET/HEAD /samsung/{frameId}/…)", filepath.Join(*dataDir, "samsung"))

	httpServer := &http.Server{Addr: *httpAddr, Handler: accessLogMiddleware(mux)}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := broker.Serve(); err != nil {
		log.Fatalf("broker serve: %v", err)
	}
	log.Printf("Joyous MQTT broker listening on %s (bridges connect here)", *mqttAddr)

	startScheduledSendScheduler(ctx, hub)

	go func() {
		log.Printf("HTTP server listening on %s (images at http://%s)", *httpAddr, addr)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("HTTP: %v", err)
		}
	}()

	go func() {
		time.Sleep(100 * time.Millisecond)
		probeHost := "127.0.0.1"
		_, port, err := net.SplitHostPort(*httpAddr)
		if err != nil || port == "" {
			port = "8080"
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		base := "http://" + net.JoinHostPort(probeHost, port)
		if err := VerifyInkjoyCacheServing(ctx, base); err != nil {
			log.Printf("warn: inkjoy cache route self-check: %v", err)
		} else {
			log.Printf("inkjoy cache route self-check ok")
		}
		if err := VerifySamsungCacheServing(ctx, base); err != nil {
			log.Printf("warn: samsung cache route self-check: %v", err)
		} else {
			log.Printf("samsung cache route self-check ok")
		}
	}()

	<-ctx.Done()
	log.Println("shutting down...")

	shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	httpServer.Shutdown(shutCtx)
	broker.Close()
	devices.Save()
	if err := samsungBattery.Save(); err != nil {
		log.Printf("warn: save samsung battery history: %v", err)
	}
}

