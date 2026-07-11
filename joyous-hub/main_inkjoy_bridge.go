//go:build inkjoybridge

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
	"strings"
	"syscall"
	"time"

	"joyous-hub/bridgehub"
	"joyous-hub/inkjoybridge"
	"joyous-hub/internal/linkmeta"
	"joyous-hub/protocol"
)

func main() {
	hubMQTT := flag.String("hub-mqtt", "tcp://127.0.0.1:11883", "hub joyous MQTT broker")
	bridgeID := flag.String("bridge-id", protocol.KindInkJoy, "bridge id announced to hub")
	listenMQTT := flag.String("listen-mqtt", ":1883", "InkJoy frame MQTT broker")
	listenHTTP := flag.String("listen-http", ":18081", "HTTP listen (play relay cache)")
	upstream := flag.String("upstream", "13.39.148.101:1883", "InkJoy cloud MQTT broker")
	upstreamUsr := flag.String("upstream-usr", "", "cloud MQTT username")
	upstreamPwd := flag.String("upstream-pwd", "", "cloud MQTT password")
	upstreamAllow := flag.String("upstream-allow", inkjoybridge.DefaultUpstreamAllowCSV(), "frame→cloud allow list")
	downstreamAllow := flag.String("downstream-allow", inkjoybridge.DefaultDownstreamAllowCSV(), "cloud→frame allow list")
	intercept := flag.String("intercept", inkjoybridge.DefaultInterceptCSV(), "intercept list")
	dataDir := flag.String("data-dir", "./data-inkjoy", "bridge data directory")
	serverAddr := flag.String("server-addr", "", "public HTTP address for play URLs")
	captureDir := flag.String("capture-dir", "", "MQTT capture dir (auto/off)")
	otaDir := flag.String("ota-dir", "", "OTA capture dir (auto/off)")
	flag.Parse()

	if *upstreamUsr == "" {
		*upstreamUsr = os.Getenv("INKJOY_MQTT_USER")
	}
	if *upstreamPwd == "" {
		*upstreamPwd = os.Getenv("INKJOY_MQTT_PASSWORD")
	}
	if err := os.MkdirAll(*dataDir, 0755); err != nil {
		log.Fatalf("data-dir: %v", err)
	}

	log.Printf("inkjoy-bridge version %s", linkmeta.Version)

	devices := NewDeviceRegistry(*dataDir)
	if err := devices.Load(); err != nil {
		log.Printf("warn: load devices: %v", err)
	}
	capture := NewMessageCapture(
		resolveCaptureDir(*captureDir, *dataDir),
		inkjoybridge.ParseAllowList(*upstreamAllow),
		inkjoybridge.ParseAllowList(*downstreamAllow),
		inkjoybridge.ParseAllowList(*intercept),
	)
	otaCapture := NewOTACapture(resolveOTADir(*otaDir, *dataDir))
	mqttLog := NewMQTTLogBuffer(20)
	inkjoyCache := NewInkJoyCache(*dataDir)
	sendDelivery := NewSendDeliveryTracker()

	addr := *serverAddr
	if addr == "" {
		addr = localAddr(*listenHTTP)
	}

	imageStore, err := NewImageStoreE(*dataDir)
	if err != nil {
		log.Fatalf("image store: %v", err)
	}
	colorStore := NewColorStore(*dataDir)
	imageStore.SetColorStore(colorStore)
	overlayStore := NewOverlayStore(*dataDir)
	encodeHub := &Hub{
		images:     imageStore,
		color:      colorStore,
		overlay:    overlayStore,
		serverAddr: addr,
	}

	relay := &playRelayAdapter{hub: &Hub{inkjoy: inkjoyCache, serverAddr: addr, images: imageStore}}
	srv := inkjoybridge.NewServer(inkjoybridge.Config{
		ListenMQTT:      *listenMQTT,
		Upstream:        *upstream,
		UpstreamUsr:     *upstreamUsr,
		UpstreamPwd:     *upstreamPwd,
		UpstreamAllow:   inkjoybridge.ParseAllowList(*upstreamAllow),
		DownstreamAllow: inkjoybridge.ParseAllowList(*downstreamAllow),
		Intercept:       inkjoybridge.ParseAllowList(*intercept),
		Devices:         &inkjoyDeviceAdapter{reg: devices},
		PlayRelay:       relay,
		Capture:         &captureAdapter{c: capture},
		OTA:             &otaAdapter{o: otaCapture},
		MQTTLog:         &mqttLogAdapter{l: mqttLog},
	})
	inkjoyRetry := NewInkJoySendRetry(&Hub{
		devices: devices, sendDelivery: sendDelivery, publisher: srvPublisher{srv: srv},
	})
	srv.SetInjectedPlayCheck(isInjectedPlay, func(ackMsgid string, result int) {
		inkjoyRetry.OnPlayAck(ackMsgid, result)
	})

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var hubClient *bridgehub.Client
	var connectErr error
	hubClient, connectErr = bridgehub.Connect(bridgehub.ClientConfig{
		HubMQTT:  *hubMQTT,
		BridgeID: *bridgeID,
		Kind:     protocol.KindInkJoy,
		OnCommand: func(cmd protocol.CmdPayload) {
			handleInkJoyBridgeCommand(ctx, srv, encodeHub, devices, sendDelivery, inkjoyCache, hubClient, addr, cmd)
		},
	})
	if connectErr != nil {
		log.Fatalf("hub connect: %v", connectErr)
	}
	defer hubClient.Disconnect()

	if err := srv.Start(ctx); err != nil {
		log.Fatalf("inkjoy mqtt: %v", err)
	}

	mux := http.NewServeMux()
	registerInkJoyBridgeHTTP(mux, inkjoyCache)
	httpServer := &http.Server{Addr: *listenHTTP, Handler: mux}
	go func() {
		log.Printf("inkjoy-bridge HTTP on %s", *listenHTTP)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("HTTP: %v", err)
		}
	}()

	go inkjoyBridgeSyncLoop(ctx, hubClient, devices, mqttLog)

	<-ctx.Done()
	log.Println("inkjoy-bridge shutting down...")
	shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	httpServer.Shutdown(shutCtx)
	devices.Save()
}

type srvPublisher struct{ srv *inkjoybridge.Server }

func (p srvPublisher) Publish(topic string, payload []byte) error {
	return p.srv.Publish(topic, payload)
}

func registerInkJoyBridgeHTTP(mux *http.ServeMux, cache *InkJoyCache) {
	mux.HandleFunc("GET /inkjoy/{mac}/{file}", func(w http.ResponseWriter, r *http.Request) {
		if cache == nil {
			http.NotFound(w, r)
			return
		}
		name := strings.TrimSuffix(r.PathValue("file"), ".bin")
		cache.ServeHTTP(w, r, r.PathValue("mac"), name)
	})
}

func inkjoyBridgeSyncLoop(ctx context.Context, client *bridgehub.Client, reg *DeviceRegistry, logBuf *MQTTLogBuffer) {
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			devs := bridgeDevicesFromRegistry(reg, DeviceTypeInkJoy)
			_ = client.PublishDevices(devs)
			if logBuf != nil {
				local, upstream := logBuf.Snapshot()
				lb, _ := json.Marshal(local)
				ub, _ := json.Marshal(upstream)
				_ = client.PublishMQTTLogs(protocol.MQTTLogsPayload{Local: lb, Upstream: ub})
			}
			_ = client.PublishUIState(protocol.UIStatePayload{
				Revision: int(time.Now().Unix()),
				State:    marshalBridgeUI(devs),
			})
		}
	}
}

func handleInkJoyBridgeCommand(
	ctx context.Context,
	srv *inkjoybridge.Server,
	encodeHub *Hub,
	devices *DeviceRegistry,
	sendDelivery *SendDeliveryTracker,
	cache *InkJoyCache,
	hubClient *bridgehub.Client,
	bridgeAddr string,
	cmd protocol.CmdPayload,
) {
	switch cmd.Cmd {
	case protocol.CmdSendImage:
		var body protocol.SendImageBody
		if err := json.Unmarshal(cmd.Body, &body); err != nil {
			log.Printf("inkjoy-bridge send.image: bad body: %v", err)
			return
		}
		dev, ok := devices.Get(cmd.DeviceID)
		if !ok || dev.Type != DeviceTypeInkJoy {
			return
		}
		portrait := dev.Portrait
		bin, err := bridgeEncodeInkJoy(ctx, encodeHub, body, dev)
		if err != nil {
			log.Printf("inkjoy-bridge encode: %v", err)
			if body.SendID != "" && sendDelivery != nil {
				sendDelivery.Fail(body.SendID)
			}
			return
		}
		cacheFile := imageBinFilename(body.ImageID, body.OverlayToken, portrait)
		cacheName := strings.TrimSuffix(cacheFile, ".bin")
		if err := cache.Save(dev.MAC, cacheName, bin); err != nil {
			log.Printf("inkjoy-bridge cache: %v", err)
			return
		}
		imgURL := fmt.Sprintf("http://%s/inkjoy/%s/%s.bin", bridgeAddr, dev.MAC, cacheName)
		payload, msgid := buildPlayPayload(dev.MAC, imgURL)
		if body.SendID != "" {
			sendDelivery.UnbindInkJoy(body.SendID)
			sendDelivery.BindInkJoy(body.SendID, msgid)
		}
		registerInjectedPlay(msgid)
		_ = srv.PublishToFrame(dev.MAC, payload)
	case protocol.CmdRefresh:
		dev, ok := devices.Get(cmd.DeviceID)
		if !ok {
			return
		}
		_ = srv.PublishToFrame(dev.MAC, buildImageRefreshPayload(dev.MAC))
	case protocol.CmdSleep:
		dev, ok := devices.Get(cmd.DeviceID)
		if !ok {
			return
		}
		_ = srv.PublishToFrame(dev.MAC, buildWifiSleepPayloadFromBody(dev.MAC, cmd.Body))
	case protocol.CmdRedirect:
		dev, ok := devices.Get(cmd.DeviceID)
		if !ok {
			return
		}
		var cfg inkjoybridge.UpstreamConfig
		if json.Unmarshal(cmd.Body, &cfg) == nil && cfg.Host != "" {
			_ = srv.PublishToFrame(dev.MAC, inkjoybridge.BuildMQTTConfigPayload(dev.MAC, cfg))
		}
	case protocol.CmdBLEScan, protocol.CmdBLEAdopt:
		log.Printf("inkjoy-bridge: BLE commands handled locally (not yet forwarded via MQTT cmd)")
	case "mqtt.publish":
		var body struct {
			Topic   string `json:"topic"`
			Payload string `json:"payload"`
		}
		_ = json.Unmarshal(cmd.Body, &body)
		_ = srv.Publish(body.Topic, []byte(body.Payload))
	default:
		log.Printf("inkjoy-bridge: unhandled cmd %q", cmd.Cmd)
	}
	if hubClient != nil {
		_ = hubClient.PublishDevices(bridgeDevicesFromRegistry(devices, DeviceTypeInkJoy))
	}
}
