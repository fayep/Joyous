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
	cfgPath := configPathFromArgs(os.Args)
	if cfgPath == "" {
		var err error
		cfgPath, err = DefaultInkJoyConfigPath()
		if err != nil {
			log.Fatalf("config path: %v", err)
		}
	}
	fileCfg, err := LoadInkJoyBridgeConfig(cfgPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	if _, err := os.Stat(cfgPath); err == nil {
		log.Printf("config: %s", cfgPath)
	}
	applyInkJoyEnvOverrides(&fileCfg)

	hubMQTT := flag.String("hub-mqtt", fileCfg.HubMQTT, "hub joyous MQTT broker")
	bridgeID := flag.String("bridge-id", fileCfg.BridgeID, "bridge id announced to hub")
	listenMQTT := flag.String("listen-mqtt", fileCfg.ListenMQTT, "InkJoy frame MQTT broker")
	hubHTTP := flag.String("hub-http", fileCfg.HubHTTP, "Joyous hub HTTP base URL for play relay")
	upstream := flag.String("upstream", fileCfg.Upstream, "InkJoy cloud MQTT broker")
	upstreamUsr := flag.String("upstream-usr", fileCfg.UpstreamUsr, "cloud MQTT username")
	upstreamPwd := flag.String("upstream-pwd", fileCfg.UpstreamPwd, "cloud MQTT password")
	upstreamAllow := flag.String("upstream-allow", fileCfg.UpstreamAllow, "frame→cloud allow list")
	downstreamAllow := flag.String("downstream-allow", fileCfg.DownstreamAllow, "cloud→frame allow list")
	intercept := flag.String("intercept", fileCfg.Intercept, "intercept list")
	dataDir := flag.String("data-dir", fileCfg.DataDir, "bridge data directory")
	hubDataDir := flag.String("hub-data-dir", fileCfg.HubDataDir, "hub data directory (play relay cache + device import)")
	captureDir := flag.String("capture-dir", fileCfg.CaptureDir, "MQTT capture dir (auto/off)")
	otaDir := flag.String("ota-dir", fileCfg.OTADir, "OTA capture dir (auto/off)")
	flag.String("config", cfgPath, "config file")
	flag.Parse()

	if *bridgeID == "" {
		*bridgeID = protocol.KindInkJoy
	}
	*hubDataDir = reconcileHubDataDir(*hubDataDir)
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
	if n, err := devices.ImportInkJoyFrom(*hubDataDir); err != nil {
		log.Printf("warn: import hub InkJoy devices: %v", err)
	} else if n > 0 {
		log.Printf("imported %d InkJoy device(s) from hub data dir %s", n, *hubDataDir)
		if err := devices.Save(); err != nil {
			log.Printf("warn: save imported devices: %v", err)
		}
	}
	capture := NewMessageCapture(
		resolveCaptureDir(*captureDir, *dataDir),
		inkjoybridge.ParseAllowList(*upstreamAllow),
		inkjoybridge.ParseAllowList(*downstreamAllow),
		inkjoybridge.ParseAllowList(*intercept),
	)
	otaCapture := NewOTACapture(resolveOTADir(*otaDir, *dataDir))
	mqttLog := NewMQTTLogBuffer(20)
	sendDelivery := NewSendDeliveryTracker()

	relayCacheDir := strings.TrimSpace(*hubDataDir)
	if relayCacheDir == "" {
		log.Printf("warn: hub_data_dir unset; cloud play relay cache stays in bridge data_dir (frames may not fetch it)")
		relayCacheDir = *dataDir
	}
	hubInkjoyCache := NewInkJoyCache(relayCacheDir)
	hubPlayAddr := hubHTTPHostPort(*hubHTTP)
	hubPlayIP := resolvedLANIP(hubPlayAddr)

	imageStore, err := NewImageStoreE(*dataDir)
	if err != nil {
		log.Fatalf("image store: %v", err)
	}
	colorStore := NewColorStore(*dataDir)
	imageStore.SetColorStore(colorStore)
	overlayStore := NewOverlayStore(*dataDir)
	displayPreview := NewDisplayPreviewStore(*dataDir)
	encodeHub := &Hub{
		images:     imageStore,
		color:      colorStore,
		overlay:    overlayStore,
		serverAddr: hubPlayAddr,
		hubIP:      hubPlayIP,
	}

	relay := &playRelayAdapter{hub: &Hub{
		inkjoy:         hubInkjoyCache,
		serverAddr:     hubPlayAddr,
		hubIP:          hubPlayIP,
		devices:        devices,
		displayPreview: displayPreview,
		images:         imageStore,
	}}
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

	mqttHost := bridgeMQTTHost(*listenMQTT)
	mqttPort := bridgeMQTTPort(*listenMQTT)
	mux := http.NewServeMux()
	bridgeUI := newInkJoyBridgeUI(mux, devices, srv, mqttLog, mqttHost, mqttPort)
	_ = bridgeUI

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var hubClient *bridgehub.Client
	var connectErr error
	hubClient, connectErr = bridgehub.Connect(bridgehub.ClientConfig{
		HubMQTT:  *hubMQTT,
		BridgeID: *bridgeID,
		Kind:     protocol.KindInkJoy,
		Hello: protocol.HelloPayload{
			Kind:         protocol.KindInkJoy,
			Capabilities: []string{protocol.CapConfigUI},
			ListenMQTT:   *listenMQTT,
		},
		UIHTTP: bridgeUI,
		OnCommand: func(cmd protocol.CmdPayload) {
			handleInkJoyBridgeCommand(ctx, srv, encodeHub, devices, sendDelivery, inkjoyRetry, hubInkjoyCache, hubClient, cmd)
		},
	})
	if connectErr != nil {
		log.Fatalf("hub connect: %v", connectErr)
	}
	defer hubClient.Disconnect()

	inkjoyRetry.SetSendCompleteNotifier(func(body protocol.SendCompletePayload) {
		if err := hubClient.PublishSendComplete(body); err != nil {
			log.Printf("inkjoy-bridge send.complete publish: %v", err)
		}
	})
	bridgeCtx := ctx
	inkjoyRetry.SetResender(func(entry *inkjoyRetryEntry) error {
		dev, ok := devices.Get(entry.deviceID)
		if !ok {
			return fmt.Errorf("device %q not found", entry.deviceID)
		}
		return bridgeDeliverInkJoyImage(bridgeCtx, srv, encodeHub, sendDelivery, inkjoyRetry, hubInkjoyCache, dev, protocol.SendImageBody{
			ImageID:      entry.imageID,
			OverlayToken: entry.overlayToken,
			SendID:       entry.sendID,
			HubBaseURL:   entry.hubBaseURL,
		})
	})

	if err := srv.Start(ctx); err != nil {
		log.Fatalf("inkjoy mqtt: %v", err)
	}
	log.Printf("inkjoy-bridge play relay via hub http://%s (cache %s)", hubPlayAddr, relayCacheDir)
	{
		probeCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
		err := VerifyInkjoyCacheServing(probeCtx, *hubHTTP)
		cancel()
		if err != nil {
			log.Printf("warn: hub inkjoy cache route check failed: %v (upgrade/restart joyous-hub before sending)", err)
		} else {
			log.Printf("hub inkjoy cache route check ok")
		}
	}

	go inkjoyBridgeSyncLoop(ctx, hubClient, devices)

	<-ctx.Done()
	log.Println("inkjoy-bridge shutting down...")
	devices.Save()
}

type srvPublisher struct{ srv *inkjoybridge.Server }

func (p srvPublisher) Publish(topic string, payload []byte) error {
	return p.srv.Publish(topic, payload)
}

func inkjoyBridgeSyncLoop(ctx context.Context, client *bridgehub.Client, reg *DeviceRegistry) {
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			devs := bridgeDevicesFromRegistry(reg, DeviceTypeInkJoy)
			_ = client.PublishDevices(devs)
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
	inkjoyRetry *InkJoySendRetry,
	cache *InkJoyCache,
	hubClient *bridgehub.Client,
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
		if err := bridgeDeliverInkJoyImage(ctx, srv, encodeHub, sendDelivery, inkjoyRetry, cache, dev, body); err != nil {
			log.Printf("inkjoy-bridge send.image: %v", err)
			if body.SendID != "" && sendDelivery != nil {
				sendDelivery.Fail(body.SendID)
				if hubClient != nil {
					_ = hubClient.PublishSendComplete(protocol.SendCompletePayload{
						SendID:   body.SendID,
						DeviceID: dev.ID,
						Success:  false,
						Detail:   err.Error(),
					})
				}
			}
		}
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

func bridgeDeliverInkJoyImage(
	ctx context.Context,
	srv *inkjoybridge.Server,
	encodeHub *Hub,
	sendDelivery *SendDeliveryTracker,
	inkjoyRetry *InkJoySendRetry,
	cache *InkJoyCache,
	dev *Device,
	body protocol.SendImageBody,
) error {
	bin, err := bridgeEncodeInkJoy(ctx, encodeHub, body, dev)
	if err != nil {
		return fmt.Errorf("encode: %w", err)
	}
	cacheName := inkjoyAlbumCacheName(body.ImageID, body.OverlayToken, dev.Portrait)
	if err := cache.Save(dev.MAC, cacheName, bin); err != nil {
		return fmt.Errorf("cache: %w", err)
	}
	cachePath := cache.FilePath(dev.MAC, cacheName)
	hubHost := dev.HubIP
	if hubHost == "" {
		hubHost = encodeHub.hubIP
	}
	imgURL := inkjoyPlayURL(body.HubBaseURL, dev.IP, dev.MAC, cacheName, hubHost)
	log.Printf("[%s] cache write %s (%dB) play url %s hub_host=%s", dev.MAC, cachePath, len(bin), imgURL, hubHost)
	probeCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	err = ProbeHubURL(probeCtx, imgURL, int64(len(bin)))
	cancel()
	if err != nil {
		return fmt.Errorf("hub cache probe %s: %w", imgURL, err)
	}
	log.Printf("[%s] hub cache probe ok image=%s", dev.MAC, body.ImageID)
	payload, msgid := buildPlayPayload(dev.MAC, imgURL)
	if body.SendID != "" && sendDelivery != nil {
		sendDelivery.UnbindInkJoy(body.SendID)
		sendDelivery.BindInkJoy(body.SendID, msgid)
		if inkjoyRetry != nil {
			inkjoyRetry.TrackFromBridge(body.SendID, dev.ID, body.ImageID, body.OverlayToken, body.HubBaseURL)
		}
	}
	registerInjectedPlay(msgid)
	if err := srv.PublishToFrame(dev.MAC, payload); err != nil {
		return err
	}
	return nil
}
