package main

// InkJoy frame tool — MQTT spy/spoofer + authenticated image push.
//
// Usage:
//   go run inkjoy.go                                    # listen only
//   go run inkjoy.go -spoof                             # send login+heart as frame
//   go run inkjoy.go -spoof -fw 0.1.0                  # old firmware → trigger autoOta
//   go run inkjoy.go -push /path/to/photo.jpg          # push image to frame via server
//   go run inkjoy.go -push photo.jpg -listen 120       # push + watch play_ack progress

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

// ── Constants ──────────────────────────────────────────────────────────────

const (
	mqttHost = "13.39.148.101"
	mqttPort = 1883
	mqttUser = "REDACTED_MQTT_USER"
	mqttPass = "REDACTED_MQTT_PASSWORD"

	clientID    = "AABBCCDDEEFF"
	deviceID    = "00000000-0000-0000-0000-000000000002"
	topicReport = "/device/report/" + clientID
	topicInkjoy = "/inkjoyap/" + clientID

	baseURL = "https://app.inkjoyframe.com"
	signKey = "REDACTED_SIGN_KEY"
)

// ── REST signing ───────────────────────────────────────────────────────────

func apiSign(method, path, body string) map[string]string {
	ts := strconv.FormatInt(time.Now().UnixMilli(), 10)
	nonce := fmt.Sprintf("%x", time.Now().UnixNano())
	bh := ""
	if body != "" {
		h := sha256.Sum256([]byte(body))
		bh = fmt.Sprintf("%x", h)
	}
	msg := method + path + ts + nonce + bh
	mac := hmac.New(sha256.New, []byte(signKey))
	mac.Write([]byte(msg))
	return map[string]string{
		"X-Timestamp": ts,
		"X-Nonce":     nonce,
		"X-Signature": fmt.Sprintf("%x", mac.Sum(nil)),
	}
}

func apiDo(method, path, body, token, uid string) ([]byte, error) {
	var br io.Reader
	if body != "" {
		br = bytes.NewBufferString(body)
	}
	req, err := http.NewRequest(method, baseURL+path, br)
	if err != nil {
		return nil, err
	}
	for k, v := range apiSign(method, path, body) {
		req.Header.Set(k, v)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if uid != "" {
		req.Header.Set("uid", uid)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

// ── Auth ───────────────────────────────────────────────────────────────────

type loginResp struct {
	Code int `json:"code"`
	Data struct {
		Token string `json:"token"`
		Uid   string `json:"uid"`
	} `json:"data"`
	Msg string `json:"msg"`
}

func apiLogin(email, password string) (token, uid string, err error) {
	path := "/inkjoy/api/v1/users/loginByEmail"
	body := fmt.Sprintf(`{"email":%q,"password":%q}`, email, password)
	raw, err := apiDo("POST", path, body, "", "")
	if err != nil {
		return "", "", err
	}
	var r loginResp
	if err = json.Unmarshal(raw, &r); err != nil {
		return "", "", err
	}
	if r.Code != 0 {
		return "", "", fmt.Errorf("login failed: %s", r.Msg)
	}
	return r.Data.Token, r.Data.Uid, nil
}

// ── Presign + upload ───────────────────────────────────────────────────────

type presignResp struct {
	Code int `json:"code"`
	Data struct {
		PresignUrl string `json:"presignUrl"`
		Uri        string `json:"uri"`
	} `json:"data"`
	Msg string `json:"msg"`
}

func apiPresign(suffix, token, uid string) (presignURL, uri string, err error) {
	path := "/inkjoy/api/v1/file/gen_presign_url"
	body := fmt.Sprintf(`{"type":"image","suffix":%q}`, suffix)
	raw, err := apiDo("POST", path, body, token, uid)
	if err != nil {
		return "", "", err
	}
	var r presignResp
	if err = json.Unmarshal(raw, &r); err != nil {
		return "", "", err
	}
	if r.Code != 0 {
		return "", "", fmt.Errorf("presign failed: %s", r.Msg)
	}
	return r.Data.PresignUrl, r.Data.Uri, nil
}

func uploadFile(presignURL, filePath string) error {
	ext := filepath.Ext(filePath)
	contentType := mime.TypeByExtension(ext)
	if contentType == "" {
		contentType = "image/jpeg"
	}
	f, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer f.Close()
	fi, _ := f.Stat()
	req, err := http.NewRequest("PUT", presignURL, f)
	if err != nil {
		return err
	}
	req.ContentLength = fi.Size()
	req.Header.Set("Content-Type", contentType)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("S3 PUT returned %s", resp.Status)
	}
	return nil
}

// ── Play (trigger frame) ───────────────────────────────────────────────────

type playResp struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
}

func apiPlay(imgURI, token, uid string) error {
	path := "/inkjoy/api/v1/device/play"
	body := fmt.Sprintf(`{"deviceId":%q,"imgUri":%q,"binUri":%q,"orientation":0}`,
		deviceID, imgURI, imgURI)
	raw, err := apiDo("POST", path, body, token, uid)
	if err != nil {
		return err
	}
	var r playResp
	if err = json.Unmarshal(raw, &r); err != nil {
		return err
	}
	if r.Code != 0 {
		return fmt.Errorf("play failed (code %d): %s", r.Code, r.Msg)
	}
	return nil
}

// ── MQTT frame messages ────────────────────────────────────────────────────

type loginData struct {
	Ver        string `json:"ver"`
	Statype    int    `json:"statype"`
	SleepMode  int    `json:"sleep_mode"`
	SleepBegin string `json:"sleep_begin_time"`
	SleepEnd   string `json:"sleep_end_time"`
	Inkjoy     bool   `json:"inkjoy"`
}
type loginMsg struct {
	Action   string    `json:"action"`
	Clientid string    `json:"clientid"`
	Msgid    string    `json:"msgid"`
	Stamac   string    `json:"stamac"`
	Data     loginData `json:"data"`
}
type heartData struct {
	Type         int    `json:"type"`
	Ack          int    `json:"ack"`
	Wifi         string `json:"wifi"`
	WifiName     string `json:"wifi_name"`
	Ble          string `json:"ble"`
	Tf           string `json:"tf"`
	Tfsize       int    `json:"tfsize"`
	Tfused       int    `json:"tfused"`
	Orientation  int    `json:"orientation"`
	Battery      int    `json:"battery"`
	WifiListenIv int    `json:"wifi_listen_iv"`
	WifiRssi     int    `json:"wifi_rssi"`
	WifiCh       int    `json:"wifi_ch"`
	BleRssi      int    `json:"ble_rssi"`
	Version      string `json:"version"`
}
type heartMsg struct {
	Action   string    `json:"action"`
	Clientid string    `json:"clientid"`
	Msgid    string    `json:"msgid"`
	Stamac   string    `json:"stamac"`
	Data     heartData `json:"data"`
}
type shutdownMsg struct {
	Action string `json:"action"`
	Msgid  string `json:"msgid"`
	Stamac string `json:"stamac"`
}

func msgid() string { return strconv.FormatInt(time.Now().UnixMilli(), 10) }

func makeLogin(fw string) []byte {
	b, _ := json.Marshal(loginMsg{
		Action: "login", Clientid: clientID, Msgid: msgid(), Stamac: clientID,
		Data: loginData{Ver: fmt.Sprintf("M H:2 F:%s", fw), Statype: 3,
			SleepMode: 2, SleepBegin: "07:00", SleepEnd: "13:00", Inkjoy: true},
	})
	return b
}

func makeHeart(fw string) []byte {
	b, _ := json.Marshal(heartMsg{
		Action: "heart", Clientid: clientID, Msgid: msgid(), Stamac: clientID,
		Data: heartData{Type: 3, Ack: 1, Wifi: "on", WifiName: "ExampleWiFi",
			Ble: "off", Tf: "absent", Battery: 73, WifiListenIv: 50,
			WifiRssi: -45, WifiCh: 1, Version: fw},
	})
	return b
}

func makeShutdown() []byte {
	b, _ := json.Marshal(shutdownMsg{Action: "shutdown", Msgid: msgid(), Stamac: clientID})
	return b
}

// ── Main ───────────────────────────────────────────────────────────────────

func main() {
	fw         := flag.String("fw", "0.5.6", "firmware version to report")
	spoof      := flag.Bool("spoof", false, "publish login+heart as the frame")
	pushFile   := flag.String("push", "", "image file to push to the frame (requires -email/-password)")
	email      := flag.String("email", "user@example.com", "account email")
	password   := flag.String("password", "", "account password")
	listenSecs := flag.Int("listen", 120, "seconds to listen for server responses")
	flag.Parse()

	// ── Image push path ────────────────────────────────────────────────────
	if *pushFile != "" {
		if *password == "" {
			log.Fatal("-password required for -push")
		}
		log.Printf("Logging in as %s ...", *email)
		token, uid, err := apiLogin(*email, *password)
		if err != nil {
			log.Fatalf("Login: %v", err)
		}
		log.Printf("Authenticated (uid=%s)", uid)

		ext := filepath.Ext(*pushFile)
		if ext == "" {
			ext = ".jpg"
		}
		suffix := ext[1:] // strip leading dot

		log.Printf("Getting presigned upload URL (suffix=%s) ...", suffix)
		presignURL, uri, err := apiPresign(suffix, token, uid)
		if err != nil {
			log.Fatalf("Presign: %v", err)
		}
		log.Printf("Uploading %s → S3 ...", *pushFile)
		if err := uploadFile(presignURL, *pushFile); err != nil {
			log.Fatalf("Upload: %v", err)
		}
		log.Printf("Uploaded → uri=%s", uri)

		log.Printf("Triggering play on device %s ...", deviceID)
		if err := apiPlay(uri, token, uid); err != nil {
			log.Fatalf("Play: %v", err)
		}
		log.Printf("Play triggered — watching for play_ack on MQTT ...")
	}

	// ── MQTT spy/spoof path ────────────────────────────────────────────────
	broker := fmt.Sprintf("tcp://%s:%d", mqttHost, mqttPort)
	spyID  := fmt.Sprintf("InkJoyAndroid_%x", time.Now().UnixNano()&0xffffffff)

	opts := mqtt.NewClientOptions().
		AddBroker(broker).
		SetClientID(spyID).
		SetUsername(mqttUser).
		SetPassword(mqttPass).
		SetCleanSession(true).
		SetAutoReconnect(true).
		SetOnConnectHandler(func(c mqtt.Client) {
			log.Printf("MQTT connected as %s", spyID)
			for _, topic := range []string{topicInkjoy, topicReport} {
				tok := c.Subscribe(topic, 1, onMessage)
				tok.Wait()
				if tok.Error() != nil {
					log.Printf("  subscribe %s: %v", topic, tok.Error())
				} else {
					log.Printf("  subscribed: %s", topic)
				}
			}
		}).
		SetConnectionLostHandler(func(c mqtt.Client, err error) {
			log.Printf("MQTT connection lost: %v", err)
		})

	client := mqtt.NewClient(opts)
	if tok := client.Connect(); tok.Wait() && tok.Error() != nil {
		log.Fatalf("MQTT connect failed: %v", tok.Error())
	}

	if *spoof {
		time.Sleep(500 * time.Millisecond)
		login := makeLogin(*fw)
		log.Printf("→ login [fw=%s]: %s", *fw, login)
		client.Publish(topicReport, 1, false, login).Wait()
		time.Sleep(500 * time.Millisecond)
		heart := makeHeart(*fw)
		log.Printf("→ heart [fw=%s]: %s", *fw, heart)
		client.Publish(topicReport, 1, false, heart).Wait()

		go func() {
			ticker := time.NewTicker(60 * time.Second)
			defer ticker.Stop()
			for range ticker.C {
				if !client.IsConnected() {
					return
				}
				client.Publish(topicReport, 1, false, makeHeart(*fw))
			}
		}()
	}

	log.Printf("Listening for %ds ...", *listenSecs)
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	select {
	case <-time.After(time.Duration(*listenSecs) * time.Second):
	case <-sig:
	}

	if *spoof {
		sd := makeShutdown()
		log.Printf("→ shutdown: %s", sd)
		client.Publish(topicReport, 1, false, sd).Wait()
		time.Sleep(200 * time.Millisecond)
	}
	client.Disconnect(500)
	log.Println("Done.")
}

func onMessage(_ mqtt.Client, msg mqtt.Message) {
	payload := msg.Payload()
	var pretty map[string]any
	if json.Unmarshal(payload, &pretty) == nil {
		b, _ := json.MarshalIndent(pretty, "", "  ")
		fmt.Printf("\n[%s] %s\n%s\n", time.Now().Format("15:04:05"), msg.Topic(), b)
	} else {
		fmt.Printf("\n[%s] %s  raw: %q\n", time.Now().Format("15:04:05"), msg.Topic(), payload)
	}
}
