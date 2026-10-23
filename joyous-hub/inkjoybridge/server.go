package inkjoybridge

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	mochi "github.com/mochi-mqtt/server/v2"
	"github.com/mochi-mqtt/server/v2/hooks/auth"
	"github.com/mochi-mqtt/server/v2/listeners"
	"github.com/mochi-mqtt/server/v2/packets"
)

// DeviceEvents receives frame telemetry for hub sync.
type DeviceEvents interface {
	MarkConnected(mac string)
	MarkDisconnected(mac string)
	UpdateLogin(mac string, info LoginInfo)
	UpdateHeart(mac string, info HeartInfo)
	SetHubIP(mac string, ip string)
	OnPlayAck(ackMsgid string, result int)
	OnDeviceLogin(mac string)
	OnDeviceHeart(mac string)
	MarkShutdown(mac string, info SleepInfo)
}

// PlayRelay handles cloud play URL rewriting and external play tracking.
type PlayRelay interface {
	RewritePlay(mac string, payload []byte) ([]byte, error)
	OnExternalPlay(mac string, payload []byte)
}

// CaptureSink records unknown or intercepted MQTT traffic.
type CaptureSink interface {
	RecordUpstream(mac, action string, payload []byte) error
	RecordDownstream(mac, action string, payload []byte) error
	RecordIntercepted(mac, action string, payload []byte) error
}

// OTASink handles intercepted OTA/FPGA payloads.
type OTASink interface {
	Handle(mac, action string, payload []byte)
}

// MQTTLogger records MQTT traffic for debug UI.
type MQTTLogger interface {
	AddLocal(side, topic string, payload []byte, note string)
	AddUpstream(side, topic string, payload []byte, note string)
}

// Config holds InkJoy bridge startup options.
type Config struct {
	ListenMQTT      string
	Upstream        string
	UpstreamUsr     string
	UpstreamPwd     string
	UpstreamAllow   AllowList
	DownstreamAllow AllowList
	Intercept       AllowList
	Devices         DeviceEvents
	PlayRelay       PlayRelay
	Capture         CaptureSink
	OTA             OTASink
	MQTTLog         MQTTLogger
}

// Server runs the local InkJoy frame MQTT broker and upstream cloud bridges.
type Server struct {
	cfg     Config
	broker  *mochi.Server
	bridges *bridgeSet
	hook    *frameHook
}

// NewServer creates an InkJoy bridge server (call Start to listen).
func NewServer(cfg Config) *Server {
	if cfg.UpstreamAllow.set == nil {
		cfg.UpstreamAllow = DefaultUpstreamAllow()
	}
	if cfg.DownstreamAllow.set == nil {
		cfg.DownstreamAllow = DefaultDownstreamAllow()
	}
	if cfg.Intercept.set == nil {
		cfg.Intercept = DefaultIntercept()
	}
	s := &Server{cfg: cfg}
	s.broker = mochi.New(&mochi.Options{InlineClient: true})
	s.bridges = &bridgeSet{
		upstreamHost:    cfg.Upstream,
		upstreamUsr:     cfg.UpstreamUsr,
		upstreamPwd:     cfg.UpstreamPwd,
		upstreamAllow:   cfg.UpstreamAllow,
		downstreamAllow: cfg.DownstreamAllow,
		intercept:       cfg.Intercept,
		capture:         cfg.Capture,
		ota:             cfg.OTA,
		mqttLog:         cfg.MQTTLog,
		broker:          s.broker,
		devices:         cfg.Devices,
	}
	if cfg.PlayRelay != nil {
		s.bridges.onExternalPlay = cfg.PlayRelay.OnExternalPlay
		s.bridges.rewritePlay = cfg.PlayRelay.RewritePlay
	}
	s.hook = &frameHook{
		bridges:       s.bridges,
		devices:       cfg.Devices,
		upstreamAllow: cfg.UpstreamAllow,
	}
	return s
}

// Broker returns the embedded Mochi broker (for inline publish from bridge process).
func (s *Server) Broker() *mochi.Server { return s.broker }

// Start listens for frame MQTT connections.
func (s *Server) Start(ctx context.Context) error {
	_ = s.broker.AddHook(new(auth.AllowHook), nil)
	_ = s.broker.AddHook(s.hook, nil)
	tcpListener := listeners.NewTCP(listeners.Config{
		ID:      "tcp",
		Address: s.cfg.ListenMQTT,
	})
	if err := s.broker.AddListener(tcpListener); err != nil {
		return err
	}
	if err := s.broker.Serve(); err != nil {
		return err
	}
	log.Printf("inkjoy-bridge: frame MQTT listening on %s", s.cfg.ListenMQTT)
	go func() {
		<-ctx.Done()
		s.broker.Close()
	}()
	return nil
}

// Publish sends a command to a frame on /inkjoyap/{mac} at QoS1 so the
// broker retries while the frame is momentarily radio-napping (modem-sleep).
// Everything sent this way is bridge-originated (cloud traffic is relayed
// separately), so its msgid is recorded and the frame's responses to it are
// kept from the cloud — see frameHook.OnPublished.
func (s *Server) Publish(topic string, payload []byte) error {
	if s.cfg.MQTTLog != nil {
		s.cfg.MQTTLog.AddLocal("bridge→frame", topic, payload, "")
	}
	s.bridges.markInjected(MQTTMsgid(payload))
	return s.broker.Publish(topic, payload, false, 1)
}

// injectedTTL is how long a bridge-originated message's responses are kept
// from the cloud (a play's display_done can follow its send by minutes).
const injectedTTL = time.Hour

func (bs *bridgeSet) markInjected(msgid string) {
	if msgid == "" {
		return
	}
	bs.injected.Store(msgid, struct{}{})
	time.AfterFunc(injectedTTL, func() { bs.injected.Delete(msgid) })
}

func (bs *bridgeSet) isInjected(msgid string) bool {
	_, ok := bs.injected.Load(msgid)
	return ok
}

// PublishToFrame publishes to /inkjoyap/{mac}.
func (s *Server) PublishToFrame(mac string, payload []byte) error {
	return s.Publish("/inkjoyap/"+mac, payload)
}

// ── upstream bridge set ─────────────────────────────────────────────────────

type bridgeSet struct {
	upstreamHost    string
	upstreamUsr     string
	upstreamPwd     string
	upstreamAllow   AllowList
	downstreamAllow AllowList
	intercept       AllowList
	capture         CaptureSink
	ota             OTASink
	mqttLog         MQTTLogger
	broker          *mochi.Server
	devices         DeviceEvents
	onExternalPlay  func(mac string, payload []byte)
	rewritePlay     func(mac string, payload []byte) ([]byte, error)
	m               map[string]*upstreamBridge
	localOnly       sync.Map // mac → struct{} for Seeed H:9 etc. (no cloud upstream)
	acks            loginAcks
	injected        sync.Map // msgid → struct{} of bridge-originated frame messages
	held            heldPlays
}

func (bs *bridgeSet) markLocalOnly(mac string) {
	bs.localOnly.Store(mac, struct{}{})
}

func (bs *bridgeSet) isLocalOnly(mac string) bool {
	_, ok := bs.localOnly.Load(mac)
	return ok
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

	mu    sync.Mutex
	queue [][]byte // frame→cloud messages held until the cloud link is up
}

// upstreamQueueMax bounds the frame→cloud messages held while the cloud link
// connects (a frame sends a handful — login, heart, reports — on connect).
const upstreamQueueMax = 32

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

	brokers := upstreamBrokers(host)
	var attempted atomic.Value // broker URL of the latest connection attempt
	opts := mqtt.NewClientOptions()
	for _, u := range brokers {
		opts.AddBroker(u)
	}
	opts.SetTLSConfig(upstreamTLSConfig()).
		SetConnectionAttemptHandler(func(broker *url.URL, tlsCfg *tls.Config) *tls.Config {
			attempted.Store(broker.String())
			return tlsCfg
		}).
		SetClientID(b.mac).
		SetUsername(usr).
		SetPassword(pwd).
		SetAutoReconnect(true).
		SetOnConnectHandler(func(c mqtt.Client) {
			via, _ := attempted.Load().(string)
			if len(brokers) > 1 && via != brokers[0] {
				log.Printf("[%s] upstream connected to %s — MQTTS %s FAILED, using plain fallback", b.mac, via, brokers[0])
			} else {
				log.Printf("[%s] upstream connected to %s", b.mac, via)
			}
			c.Subscribe("/inkjoyap/"+b.mac, 0, b.onCloudMessage)
			b.flushQueue(c)
		}).
		SetConnectionLostHandler(func(c mqtt.Client, err error) {
			log.Printf("[%s] upstream disconnected: %v", b.mac, err)
		})

	client := mqtt.NewClient(opts)
	b.mu.Lock()
	b.client = client
	b.mu.Unlock()
	tok := client.Connect()
	tok.Wait()
	if err := tok.Error(); err != nil {
		log.Printf("[%s] upstream connect error: %v", b.mac, err)
	}
}

func (b *upstreamBridge) onCloudMessage(_ mqtt.Client, msg mqtt.Message) {
	b.handleCloudPayload(msg.Payload())
}

// ReplayCloudMessage feeds a saved cloud→frame message for mac through the
// same path as if the cloud had just sent it: intercepts, the play relay's
// download/cache/rewrite, held delivery, and the frame's responses going
// back to the cloud (it is not marked bridge-originated).
func (s *Server) ReplayCloudMessage(mac string, payload []byte) {
	log.Printf("[%s] replaying cloud %s %s", mac, MQTTAction(payload), MQTTMsgid(payload))
	s.bridges.get(mac).handleCloudPayload(payload)
}

func (b *upstreamBridge) handleCloudPayload(payload []byte) {
	action := MQTTAction(payload)

	if ShouldIntercept(action, b.set.intercept) {
		b.set.logCloudIn(b.mac, payload, "intercepted")
		writeCaptureErr("intercept", b.set.capture, b.set.captureRecordIntercepted(b.mac, action, payload))
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
			ack := BuildAckPayload(b.mac, "mqtt_config_ack", MQTTMsgid(payload), nil)
			b.set.logCloudOut(b.mac, ack, "synthetic ack")
			b.client.Publish("/device/report/"+b.mac, 0, false, ack)
		case "wifi_sleep":
			log.Printf("[%s] wifi_sleep from cloud: suppressed, sending ack", b.mac)
			ack := BuildAckPayload(b.mac, "wifi_sleep_ack", MQTTMsgid(payload), nil)
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
		writeCaptureErr("downstream", b.set.capture, b.set.captureRecordDownstream(b.mac, action, payload))
		b.set.logCloudIn(b.mac, payload, "dropped")
		return
	}

	writeCaptureErr("downstream", b.set.capture, b.set.captureRecordDownstream(b.mac, action, payload))
	b.set.logCloudIn(b.mac, payload, "")
	if action == "wf_update" {
		// Rare and worth keeping: the waveform file's URL, size and CRC.
		log.Printf("[%s] wf_update from cloud (forwarded): %s", b.mac, payload)
	}
	if action == "login_ack" {
		b.set.noteCloudLoginAck(b.mac, payload)
	}

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
		b.set.mqttLog.AddLocal("bridge→frame", topic, forwardPayload, note)
	}
	if action == "play" {
		b.set.deliverCloudPlay(b.mac, topic, forwardPayload)
	} else {
		b.set.broker.Publish(topic, forwardPayload, false, 1)
	}
	if action == "play" && b.set.onExternalPlay != nil {
		b.set.onExternalPlay(b.mac, forwardPayload)
	}
}

func (bs *bridgeSet) captureRecordIntercepted(mac, action string, payload []byte) error {
	if bs.capture == nil {
		return nil
	}
	return bs.capture.RecordIntercepted(mac, action, payload)
}

func (bs *bridgeSet) captureRecordDownstream(mac, action string, payload []byte) error {
	if bs.capture == nil {
		return nil
	}
	return bs.capture.RecordDownstream(mac, action, payload)
}

// forward relays a frame message to the cloud. While the cloud link is still
// connecting (it is opened on a frame's first message, so right after a bridge
// restart the frame's login always arrives first) the message is queued and
// sent on connect, rather than dropped.
func (b *upstreamBridge) forward(payload []byte) {
	b.mu.Lock()
	c := b.client
	if c == nil || !c.IsConnected() {
		if len(b.queue) < upstreamQueueMax {
			b.queue = append(b.queue, payload)
		}
		b.mu.Unlock()
		return
	}
	b.mu.Unlock()
	b.publishUp(c, payload)
}

func (b *upstreamBridge) flushQueue(c mqtt.Client) {
	b.mu.Lock()
	q := b.queue
	b.queue = nil
	b.mu.Unlock()
	for _, p := range q {
		b.publishUp(c, p)
	}
}

func (b *upstreamBridge) publishUp(c mqtt.Client, payload []byte) {
	b.set.logCloudOut(b.mac, payload, "")
	c.Publish("/device/report/"+b.mac, 0, false, payload)
	if MQTTAction(payload) == "login" {
		b.set.loginSentUpstream(b.mac, MQTTMsgid(payload))
	}
}

func (b *upstreamBridge) sendBlockedOTAAcks(interceptedAction string, requestPayload []byte) {
	if b.client == nil || !b.client.IsConnected() {
		return
	}
	ackMsgid := MQTTMsgid(requestPayload)
	for i, ack := range BuildBlockedOTAAcks(b.mac, interceptedAction, ackMsgid) {
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
		bs.mqttLog.AddUpstream("cloud→bridge", "/inkjoyap/"+mac, payload, note)
	}
}

func (bs *bridgeSet) logCloudOut(mac string, payload []byte, note string) {
	if bs.mqttLog != nil {
		bs.mqttLog.AddUpstream("bridge→cloud", "/device/report/"+mac, payload, note)
	}
}

func writeCaptureErr(where string, sink CaptureSink, err error) {
	if err != nil {
		log.Printf("capture %s: %v", where, err)
	}
}

// ── broker hook ───────────────────────────────────────────────────────────────

type frameHook struct {
	mochi.HookBase
	bridges       *bridgeSet
	devices       DeviceEvents
	upstreamAllow AllowList
	injectedPlay  func(ackMsgid string) bool
	onPlayAck     func(ackMsgid string, result int)
}

func (h *frameHook) ID() string { return "frame-hook" }

func (h *frameHook) Provides(b byte) bool {
	return b == mochi.OnPublished || b == mochi.OnDisconnect
}

// OnDisconnect logs why a frame's MQTT session to the bridge ended.
func (h *frameHook) OnDisconnect(cl *mochi.Client, err error, expire bool) {
	if cl == nil || cl.Net.Inline {
		return
	}
	log.Printf("[%s] frame disconnected from bridge: %v", cl.ID, err)
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
		return
	}

	action := MQTTAction(payload)
	if h.bridges.mqttLog != nil {
		h.bridges.mqttLog.AddLocal("frame→bridge", topic, payload, "")
	}
	writeCaptureErr("upstream", h.bridges.capture, h.bridges.captureRecordUpstream(mac, action, payload))

	skipUpstream := h.bridges.isLocalOnly(mac)
	// A frame response names the message it answers in data.ack_msgid. Every
	// response to something the bridge sent (a Joyous play — play_ack, then
	// display_start/display_done — or an image_refresh, overlay, wifi_sleep,
	// ...) stays local: the cloud never sent it and mustn't learn of it.
	if ackMsgid := MQTTAckMsgid(payload); ackMsgid != "" &&
		(h.bridges.isInjected(ackMsgid) || (h.injectedPlay != nil && h.injectedPlay(ackMsgid))) {
		skipUpstream = true
		if action != "play_ack" {
			log.Printf("[%s] %s suppressed (bridge-initiated)", mac, action)
		}
	}
	switch action {
	case "login":
		info, err := ParseLoginPayload(payload)
		if err == nil {
			if LocalOnlyHardware(info.HardwareModel) {
				h.bridges.markLocalOnly(mac)
				skipUpstream = true
				log.Printf("[%s] H:%d login: local-only (not publishing to InkJoy cloud)", mac, info.HardwareModel)
			}
			if h.devices != nil {
				h.devices.MarkConnected(mac)
				h.devices.UpdateLogin(mac, info)
			}
		}
		if cl != nil && cl.Net.Conn != nil && h.devices != nil {
			if localIP, _, err := net.SplitHostPort(cl.Net.Conn.LocalAddr().String()); err == nil && localIP != "" {
				h.devices.SetHubIP(mac, localIP)
			}
		}
		if h.devices != nil {
			h.devices.OnDeviceLogin(mac)
		}
		h.bridges.releaseHeldPlay(mac)
	case "heart":
		info, err := ParseHeartPayload(payload)
		if err == nil && h.devices != nil {
			h.devices.UpdateHeart(mac, info)
		}
		if h.devices != nil {
			h.devices.OnDeviceHeart(mac)
		}
	case "play_ack":
		var ack struct {
			Data struct {
				AckMsgid string `json:"ack_msgid"`
				Result   int    `json:"result"`
			} `json:"data"`
		}
		_ = json.Unmarshal(payload, &ack)
		if h.injectedPlay != nil && h.injectedPlay(ack.Data.AckMsgid) {
			log.Printf("[%s] play_ack suppressed (bridge-initiated, result=%d)", mac, ack.Data.Result)
			skipUpstream = true
			if h.onPlayAck != nil {
				h.onPlayAck(ack.Data.AckMsgid, ack.Data.Result)
			} else if h.devices != nil {
				h.devices.OnPlayAck(ack.Data.AckMsgid, ack.Data.Result)
			}
		}
		if h.devices != nil {
			h.devices.MarkConnected(mac)
		}
	case "sleep", "shutdown":
		info, _ := ParseSleepPayload(payload)
		if h.devices != nil {
			h.devices.MarkShutdown(mac, info)
		}
	case "overlay_ack":
		// Logged, not otherwise acted on -- this is a manual comparison
		// workflow (see joyous-hub's CmdSendOverlay), the operator is
		// looking at the panel, not a UI, for the actual result.
		var ack struct {
			Data struct {
				AckMsgid string `json:"ack_msgid"`
				Result   int    `json:"result"`
			} `json:"data"`
		}
		_ = json.Unmarshal(payload, &ack)
		log.Printf("[%s] overlay_ack result=%d", mac, ack.Data.Result)
		if h.devices != nil {
			h.devices.MarkConnected(mac)
		}
	default:
		if h.devices != nil {
			h.devices.MarkConnected(mac)
		}
	}

	// Ack login/heart locally only when nobody else will: local-only clones
	// have no cloud, and real-InkJoy H:3 frames relayed upstream get no cloud
	// login_ack (the cloud's newer registration needs a device-pubkey
	// attestation the relay can't satisfy), so without a local ack they
	// login-loop and never receive plays. A frame the cloud answers gets the
	// cloud's replies alone — see ackLogin.
	localOnly := skipUpstream || h.bridges.upstreamHost == ""
	switch action {
	case "login":
		h.bridges.ackLogin(mac, MQTTMsgid(payload), localOnly)
	case "heart":
		h.bridges.ackHeart(mac, MQTTMsgid(payload), localOnly)
	case "pubkey_report":
		h.bridges.noteFramePubkeyReport(mac, payload)
	}

	if !skipUpstream && h.upstreamAllow.Allows(action) && h.bridges.upstreamHost != "" {
		bridge := h.bridges.get(mac)
		bridge.forward(payload)
	}
}

func (bs *bridgeSet) publishLocalAck(mac string, payload []byte) {
	if bs.broker == nil {
		return
	}
	topic := "/inkjoyap/" + mac
	if bs.mqttLog != nil {
		bs.mqttLog.AddLocal("bridge→frame", topic, payload, "local-only self-ack")
	}
	if err := bs.broker.Publish(topic, payload, false, 1); err != nil {
		log.Printf("[%s] local-only ack publish: %v", mac, err)
	}
}

func (bs *bridgeSet) captureRecordUpstream(mac, action string, payload []byte) error {
	if bs.capture == nil {
		return nil
	}
	return bs.capture.RecordUpstream(mac, action, payload)
}

// SetInjectedPlayCheck configures play_ack suppression for bridge-initiated plays.
func (s *Server) SetInjectedPlayCheck(fn func(ackMsgid string) bool, onPlayAck func(ackMsgid string, result int)) {
	s.hook.injectedPlay = fn
	s.hook.onPlayAck = onPlayAck
}
