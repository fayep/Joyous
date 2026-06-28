package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	mochi "github.com/mochi-mqtt/server/v2"
	"github.com/mochi-mqtt/server/v2/hooks/auth"
	"github.com/mochi-mqtt/server/v2/listeners"
	"github.com/mochi-mqtt/server/v2/packets"

	"joyous-hub/internal/linkmeta"
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

	mqttAddr := flag.String("listen-mqtt", fileCfg.ListenMQTT, "local MQTT broker address")
	httpAddr := flag.String("listen-http", fileCfg.ListenHTTP, "HTTP server address")
	upstream := flag.String("upstream", fileCfg.Upstream, "upstream broker (empty = local-only)")
	upstreamUsr := flag.String("upstream-usr", fileCfg.UpstreamUsr, "upstream broker username (or INKJOY_MQTT_USER env)")
	upstreamPwd := flag.String("upstream-pwd", fileCfg.UpstreamPwd, "upstream broker password (or INKJOY_MQTT_PASSWORD env)")
	upstreamAllow := flag.String("upstream-allow", fileCfg.UpstreamAllow,
		"comma-separated frame→broker actions forwarded to cloud")
	downstreamAllow := flag.String("downstream-allow", fileCfg.DownstreamAllow,
		"comma-separated broker→frame actions forwarded to frame")
	interceptFlag := flag.String("intercept", fileCfg.Intercept,
		"comma-separated broker→frame actions handled by hub (not forwarded to frame)")
	dataDir := flag.String("data-dir", fileCfg.DataDir, "data directory for devices.json and images/")
	serverAddr := flag.String("server-addr", fileCfg.ServerAddr, "public HTTP address for image URLs (auto-detect if empty)")
	discoverSubnetsFlag := flag.String("discover-subnets", fileCfg.DiscoverSubnets, "comma-separated LAN prefixes for MDC fallback sweep (e.g. 192.168.50 or 192.168.50.0/23); SSDP multicast always runs")
	probeNetworkFlag := flag.String("probe-network", "", "test TCP connectivity to IP:1515 and exit (triggers Local Network permission)")
	logDirFlag := flag.String("log-dir", fileCfg.LogDir, "append hub logs to stdout.log and stderr.log in this directory")
	captureDirFlag := flag.String("capture-dir", fileCfg.CaptureDir, "unknown MQTT capture dir (empty/auto={data_dir}/capture, off=disabled)")
	otaDirFlag := flag.String("ota-dir", fileCfg.OTADir, "OTA/FPGA artifact dir (empty/auto={data_dir}/ota, off=disabled)")
	flag.String("config", cfgPath, "config file (default: ~/Library/Application Support/Joyous/config.yaml or ~/.config/Joyous/config.yaml)")
	flag.Parse()

	if *upstreamUsr == "" {
		*upstreamUsr = os.Getenv("INKJOY_MQTT_USER")
	}
	if *upstreamPwd == "" {
		*upstreamPwd = os.Getenv("INKJOY_MQTT_PASSWORD")
	}

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

	upstreamAllowList := ParseAllowList(*upstreamAllow)
	downstreamAllowList := ParseAllowList(*downstreamAllow)
	interceptList := ParseAllowList(*interceptFlag)
	capturePath := resolveCaptureDir(*captureDirFlag, *dataDir)
	capture := NewMessageCapture(capturePath, upstreamAllowList, downstreamAllowList, interceptList)
	if capture != nil {
		logCaptureReady(capturePath)
	}
	otaPath := resolveOTADir(*otaDirFlag, *dataDir)
	otaCapture := NewOTACapture(otaPath)
	if otaCapture != nil {
		logOTAReady(otaPath)
	}
	mqttLog := NewMQTTLogBuffer(20)
	devices := NewDeviceRegistry(*dataDir)
	if err := devices.Load(); err != nil {
		log.Printf("warn: load devices: %v", err)
	}
	imageStore := NewImageStore(*dataDir)
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

	// ── local MQTT broker ───────────────────────────────────────────────────
	broker := mochi.New(&mochi.Options{InlineClient: true})
	_ = broker.AddHook(new(auth.AllowHook), nil) // accept all connections on LAN

	tcpListener := listeners.NewTCP(listeners.Config{
		ID:      "tcp",
		Address: *mqttAddr,
	})
	if err := broker.AddListener(tcpListener); err != nil {
		log.Fatalf("broker listener: %v", err)
	}

	// ── bridges (one per frame MAC) ─────────────────────────────────────────
	bridges := &bridgeSet{
		upstreamHost:    *upstream,
		upstreamUsr:     *upstreamUsr,
		upstreamPwd:     *upstreamPwd,
		upstreamAllow:   upstreamAllowList,
		downstreamAllow: downstreamAllowList,
		intercept:       interceptList,
		capture:         capture,
		ota:             otaCapture,
		mqttLog:         mqttLog,
		broker:          broker,
		devices:         devices,
	}

	// ── broker hooks ────────────────────────────────────────────────────────
	sendDelivery := NewSendDeliveryTracker()
	frameHook := &frameHook{bridges: bridges, devices: devices, upstreamAllow: upstreamAllowList, sendDelivery: sendDelivery}
	_ = broker.AddHook(frameHook, nil)

	// ── HTTP server ─────────────────────────────────────────────────────────
	addr := *serverAddr
	if addr == "" {
		addr = localAddr(*httpAddr)
	}
	mqttPortNum := 1883
	if _, portStr, err := net.SplitHostPort(*mqttAddr); err == nil {
		fmt.Sscanf(portStr, "%d", &mqttPortNum)
	}
	// Resolve serverAddr to a non-loopback LAN IP at startup.
	// Used for BLE adoption and as fallback in play URLs before the frame's
	// first login (after which we use the socket's LocalAddr instead).
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
		publisher:      &brokerPublisher{broker: broker, mqttLog: mqttLog},
		serverAddr:     addr,
		mqttPort:       mqttPortNum,
		hubIP:          hubIP,
		mqttLog:        mqttLog,
	}
	hub.inkjoyRetry = NewInkJoySendRetry(hub)
	frameHook.inkjoyRetry = hub.inkjoyRetry
	bridges.onExternalPlay = hub.handleExternalPlay
	bridges.rewritePlay = hub.rewriteExternalPlay
	hub.migrateSamsungFramesOnStartup()

	mux := http.NewServeMux()
	registerRoutes(mux, hub)

	httpServer := &http.Server{Addr: *httpAddr, Handler: accessLogMiddleware(mux)}

	// ── start everything ────────────────────────────────────────────────────
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := broker.Serve(); err != nil {
		log.Fatalf("broker serve: %v", err)
	}
	log.Printf("MQTT broker listening on %s", *mqttAddr)

	go func() {
		log.Printf("HTTP server listening on %s (images at http://%s)", *httpAddr, addr)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("HTTP: %v", err)
		}
	}()

	startSamsungOvernightScheduler(ctx, hub)

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

// ── broker publisher ─────────────────────────────────────────────────────────

type brokerPublisher struct {
	broker  *mochi.Server
	mqttLog *MQTTLogBuffer
}

func (p *brokerPublisher) Publish(topic string, payload []byte) error {
	if p.mqttLog != nil {
		p.mqttLog.AddLocal("hub→frame", topic, payload, "")
	}
	return p.broker.Publish(topic, payload, false, 0)
}

// ── per-frame upstream bridge set ───────────────────────────────────────────

type bridgeSet struct {
	upstreamHost    string
	upstreamUsr     string
	upstreamPwd     string
	upstreamAllow   AllowList
	downstreamAllow AllowList
	intercept       AllowList
	capture         *MessageCapture
	ota             *OTACapture
	mqttLog         *MQTTLogBuffer
	broker          *mochi.Server
	devices         *DeviceRegistry
	onExternalPlay  func(mac string, payload []byte)
	rewritePlay     func(mac string, payload []byte) ([]byte, error)
	m               map[string]*upstreamBridge
}

func (bs *bridgeSet) get(mac string) *upstreamBridge {
	if bs.m == nil {
		bs.m = make(map[string]*upstreamBridge)
	}
	if b, ok := bs.m[mac]; ok {
		return b
	}
	b := &upstreamBridge{mac: mac, set: bs}
	bs.m[mac] = b
	if bs.upstreamHost != "" {
		go b.connect()
	}
	return b
}

type upstreamBridge struct {
	mac    string
	set    *bridgeSet
	client mqtt.Client
	cfg    UpstreamConfig
}

func (b *upstreamBridge) connect() {
	host := b.set.upstreamHost
	usr := b.set.upstreamUsr
	pwd := b.set.upstreamPwd
	if b.cfg.Host != "" {
		host = fmt.Sprintf("%s:%d", b.cfg.Host, b.cfg.Port)
		usr = b.cfg.Username
		pwd = b.cfg.Password
	}
	if host == "" {
		return
	}

	opts := mqtt.NewClientOptions().
		AddBroker("tcp://" + host).
		SetClientID(b.mac).
		SetUsername(usr).
		SetPassword(pwd).
		SetAutoReconnect(true).
		SetOnConnectHandler(func(c mqtt.Client) {
			log.Printf("[%s] upstream connected to %s", b.mac, host)
			c.Subscribe("/inkjoyap/"+b.mac, 0, b.onCloudMessage)
		}).
		SetConnectionLostHandler(func(c mqtt.Client, err error) {
			log.Printf("[%s] upstream disconnected: %v", b.mac, err)
		})

	b.client = mqtt.NewClient(opts)
	tok := b.client.Connect()
	tok.Wait()
	if err := tok.Error(); err != nil {
		log.Printf("[%s] upstream connect error: %v", b.mac, err)
	}
}

func (b *upstreamBridge) onCloudMessage(_ mqtt.Client, msg mqtt.Message) {
	payload := msg.Payload()
	action := mqttAction(payload)

	if ShouldIntercept(action, b.set.intercept) {
		b.set.logCloudIn(b.mac, payload, "intercepted")
		captureWriteErr("intercept", b.set.capture.RecordIntercepted(b.mac, action, payload))
		switch action {
		case "mqtt_config":
			cfg, err := ParseMQTTConfig(payload)
			if err != nil {
				log.Printf("[%s] bad mqtt_config: %v", b.mac, err)
				return
			}
			log.Printf("[%s] mqtt_config: new upstream %s:%d", b.mac, cfg.Host, cfg.Port)
			oldClient := b.client
			b.cfg = cfg
			go func() {
				if oldClient != nil {
					oldClient.Disconnect(250)
				}
				b.connect()
			}()
			ack := buildAckPayloadFor(b.mac, "mqtt_config_ack", mqttMsgid(payload), nil)
			b.set.logCloudOut(b.mac, ack, "synthetic ack")
			b.client.Publish("/device/report/"+b.mac, 0, false, ack)
		case "wifi_sleep":
			log.Printf("[%s] wifi_sleep from cloud: suppressed, sending ack", b.mac)
			ack := buildAckPayloadFor(b.mac, "wifi_sleep_ack", mqttMsgid(payload), nil)
			b.set.logCloudOut(b.mac, ack, "synthetic ack")
			b.client.Publish("/device/report/"+b.mac, 0, false, ack)
		case "ota", "fpga":
			if b.set.ota != nil {
				b.set.ota.Handle(b.mac, action, payload)
			}
			b.sendBlockedOTAAcks(action, payload)
			log.Printf("[%s] %s blocked (artifact capture)", b.mac, action)
		default:
			log.Printf("[%s] cloud→frame %q intercepted (not forwarded)", b.mac, action)
		}
		return
	}

	if !b.set.downstreamAllow.Allows(action) {
		captureWriteErr("downstream", b.set.capture.RecordDownstream(b.mac, action, payload))
		b.set.logCloudIn(b.mac, payload, "dropped")
		return
	}

	captureWriteErr("downstream", b.set.capture.RecordDownstream(b.mac, action, payload))
	b.set.logCloudIn(b.mac, payload, "")

	forwardPayload := payload
	if action == "play" && b.set.rewritePlay != nil {
		if rewritten, err := b.set.rewritePlay(b.mac, payload); err != nil {
			log.Printf("[%s] play relay: %v (forwarding original)", b.mac, err)
		} else {
			forwardPayload = rewritten
		}
	}

	topic := "/inkjoyap/" + b.mac
	if b.set.mqttLog != nil {
		note := ""
		if action == "play" && !bytes.Equal(forwardPayload, payload) {
			note = "local relay"
		}
		b.set.mqttLog.AddLocal("hub→frame", topic, forwardPayload, note)
	}
	b.set.broker.Publish(topic, forwardPayload, false, 0)
	if action == "play" && b.set.onExternalPlay != nil {
		b.set.onExternalPlay(b.mac, forwardPayload)
	}
}

func (b *upstreamBridge) forward(payload []byte) {
	if b.client == nil || !b.client.IsConnected() {
		return
	}
	b.set.logCloudOut(b.mac, payload, "")
	b.client.Publish("/device/report/"+b.mac, 0, false, payload)
}

func (b *upstreamBridge) sendBlockedOTAAcks(interceptedAction string, requestPayload []byte) {
	if b.client == nil || !b.client.IsConnected() {
		return
	}
	ackMsgid := mqttMsgid(requestPayload)
	for i, ack := range buildBlockedOTAAcks(b.mac, interceptedAction, ackMsgid) {
		note := "synthetic ota interrupted"
		if i == 0 {
			note = "synthetic ota started"
		}
		b.set.logCloudOut(b.mac, ack, note)
		b.client.Publish("/device/report/"+b.mac, 0, false, ack)
	}
}

func (bs *bridgeSet) logCloudIn(mac string, payload []byte, note string) {
	if bs.mqttLog != nil {
		bs.mqttLog.AddUpstream("cloud→hub", "/inkjoyap/"+mac, payload, note)
	}
}

func (bs *bridgeSet) logCloudOut(mac string, payload []byte, note string) {
	if bs.mqttLog != nil {
		bs.mqttLog.AddUpstream("hub→cloud", "/device/report/"+mac, payload, note)
	}
}

// ── broker hook (OnPublished) ────────────────────────────────────────────────

type frameHook struct {
	mochi.HookBase
	bridges       *bridgeSet
	devices       *DeviceRegistry
	upstreamAllow AllowList
	sendDelivery  *SendDeliveryTracker
	inkjoyRetry   *InkJoySendRetry
}

func (h *frameHook) ID() string { return "frame-hook" }

func (h *frameHook) Provides(b byte) bool {
	return b == mochi.OnPublished
}

func (h *frameHook) OnPublished(cl *mochi.Client, pk packets.Packet) {
	topic := pk.TopicName
	payload := pk.Payload

	mac, ok := ExtractTopicMAC(topic)
	if !ok {
		return
	}

	dir := TopicDirection(topic)
	if dir != DirFrameToCloud {
		return // cloud→frame publishes injected by bridges; nothing to do
	}

	action := mqttAction(payload)
	if h.bridges.mqttLog != nil {
		h.bridges.mqttLog.AddLocal("frame→hub", topic, payload, "")
	}
	captureWriteErr("upstream", h.bridges.capture.RecordUpstream(mac, action, payload))

	var env struct {
		Action string `json:"action"`
	}
	env.Action = action

	skipUpstream := false
	switch env.Action {
	case "login":
		info, err := ParseLoginPayload(payload)
		if err == nil {
			h.devices.MarkConnected(mac)
			h.devices.UpdateLogin(mac, info)
		}
		if cl != nil && cl.Net.Conn != nil {
			if localIP, _, err := net.SplitHostPort(cl.Net.Conn.LocalAddr().String()); err == nil && localIP != "" {
				h.devices.SetHubIP(mac, localIP)
			}
		}
		if h.inkjoyRetry != nil {
			h.inkjoyRetry.OnDeviceLogin(mac)
		}
	case "heart":
		info, err := ParseHeartPayload(payload)
		if err == nil {
			h.devices.UpdateHeart(mac, info)
		}
		if h.inkjoyRetry != nil {
			h.inkjoyRetry.OnDeviceHeart(mac)
		}
	case "play_ack":
		var ack struct {
			Data struct {
				AckMsgid string `json:"ack_msgid"`
				Result   int    `json:"result"`
			} `json:"data"`
		}
		json.Unmarshal(payload, &ack)
		if isInjectedPlay(ack.Data.AckMsgid) {
			log.Printf("[%s] play_ack suppressed (hub-initiated, result=%d)", mac, ack.Data.Result)
			skipUpstream = true
			if h.inkjoyRetry != nil {
				h.inkjoyRetry.OnPlayAck(ack.Data.AckMsgid, ack.Data.Result)
			} else if h.sendDelivery != nil {
				switch ack.Data.Result {
				case inkjoyAckComplete:
					h.sendDelivery.CompleteInkJoy(ack.Data.AckMsgid, true)
				case inkjoyAckInterrupted:
					h.sendDelivery.CompleteInkJoy(ack.Data.AckMsgid, false)
				}
			}
		}
		h.devices.MarkConnected(mac)
	default:
		h.devices.MarkConnected(mac) // any publish = still alive
	}

	// Forward to upstream if allowed.
	if !skipUpstream && h.upstreamAllow.Allows(env.Action) && h.bridges.upstreamHost != "" {
		bridge := h.bridges.get(mac)
		bridge.forward(payload)
	}
}

// handleRedirect sends mqtt_config to the real broker to redirect a frame to a new broker.
func (h *Hub) handleRedirect(w http.ResponseWriter, r *http.Request, deviceID string) {
	dev, ok := h.devices.Get(deviceID)
	if !ok || dev.Type != DeviceTypeInkJoy {
		http.Error(w, "inkjoy device required", http.StatusBadRequest)
		return
	}
	mac := dev.MAC
	var cfg UpstreamConfig
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil || cfg.Host == "" {
		http.Error(w, "body: {host,port,usr,pwd} required", http.StatusBadRequest)
		return
	}
	payload := BuildMQTTConfigPayload(mac, cfg)
	topic := "/inkjoyap/" + mac
	if err := h.publisher.Publish(topic, payload); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

// ── helpers ──────────────────────────────────────────────────────────────────

func localAddr(httpAddr string) string {
	_, port, _ := net.SplitHostPort(httpAddr)
	if port == "" {
		port = "8080"
	}
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return "localhost:" + port
	}
	for _, a := range addrs {
		if ipnet, ok := a.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
			if ip4 := ipnet.IP.To4(); ip4 != nil {
				s := ip4.String()
				if !strings.HasPrefix(s, "169.") {
					return s + ":" + port
				}
			}
		}
	}
	return "localhost:" + port
}
