//go:build nixplaybridge

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image/jpeg"
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
	"joyous-hub/nixplaybridge"
	"joyous-hub/protocol"
)

// playlistRefreshInterval is how often the bridge re-lists Nixplay galleries
// in the background. Playlists change rarely, so this doesn't need to be
// fast — the hub also re-triggers a refresh on demand via CmdDiscover.
const playlistRefreshInterval = 5 * time.Minute

func main() {
	cfgPath := configPathFromArgs(os.Args)
	if cfgPath == "" {
		var err error
		cfgPath, err = DefaultNixplayConfigPath()
		if err != nil {
			log.Fatalf("config path: %v", err)
		}
	}
	fileCfg, err := LoadNixplayBridgeConfig(cfgPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	if _, err := os.Stat(cfgPath); err == nil {
		log.Printf("config: %s", cfgPath)
	}

	log.Printf("nixplay-bridge version %s", linkmeta.Version)

	creds, err := nixplaybridge.LoadCredentials(fileCfg.KeychainService, fileCfg.KeychainAccount)
	if err != nil {
		log.Fatalf("nixplay credentials: %v (add them with: security add-generic-password -a %q -s %q -w)", err, fileCfg.KeychainAccount, fileCfg.KeychainService)
	}

	nx := nixplaybridge.NewClient(creds.Email, creds.Password)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := nx.SignIn(ctx); err != nil {
		log.Fatalf("nixplay signin: %v", err)
	}
	log.Printf("nixplay-bridge signed in as %s", creds.Email)

	bridgeID := fileCfg.BridgeID
	if bridgeID == "" {
		bridgeID = protocol.KindNixplay
	}

	dataDir := fileCfg.DataDir
	if dataDir == "" {
		dataDir = "./data-nixplay"
	}
	hidden := nixplaybridge.NewHiddenStore(dataDir)
	if err := hidden.Load(); err != nil {
		log.Printf("warn: load hidden galleries: %v", err)
	}

	state := &nixplayBridgeState{nx: nx, hubHTTP: fileCfg.HubHTTP, hidden: hidden}

	var hubClient *bridgehub.Client
	republish := func() { refreshNixplayPlaylists(ctx, state, hubClient) }
	mux := http.NewServeMux()
	bridgeUI := newNixplayBridgeUI(mux, state, hidden, republish)

	hubClient, err = bridgehub.Connect(bridgehub.ClientConfig{
		HubMQTT:  fileCfg.HubMQTT,
		BridgeID: bridgeID,
		Kind:     protocol.KindNixplay,
		Hello: protocol.HelloPayload{
			Kind:         protocol.KindNixplay,
			Capabilities: []string{protocol.CapConfigUI},
		},
		OnCommand: func(cmd protocol.CmdPayload) {
			handleNixplayBridgeCommand(ctx, state, hubClient, cmd)
		},
		// Gratuitously republish devices on every (re)connect — including the first — so a hub
		// restart (which drops the in-process broker's retained device state) doesn't leave the
		// Devices tab empty until the next 5-minute playlistRefreshInterval tick.
		OnReconnect: func(c *bridgehub.Client) { refreshNixplayPlaylists(ctx, state, c) },
		UIHTTP:      bridgeUI,
	})
	if err != nil {
		log.Fatalf("hub connect: %v", err)
	}
	defer hubClient.Disconnect()

	go func() {
		tick := time.NewTicker(playlistRefreshInterval)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				refreshNixplayPlaylists(ctx, state, hubClient)
			}
		}
	}()

	<-ctx.Done()
	log.Println("nixplay-bridge shutting down...")
}

// nixplayBridgeState is the bridge's in-memory view of the Nixplay account.
type nixplayBridgeState struct {
	nx      *nixplaybridge.Client
	hubHTTP string
	hidden  *nixplaybridge.HiddenStore
}

// refreshNixplayPlaylists re-lists galleries and republishes the devices.sync
// snapshot. Hidden galleries (see nixplay_bridge_ui.go) are simply omitted —
// SyncBridgeDevices on the hub deletes any bridge-owned device not present in
// a fresh snapshot, so leaving one out is enough to remove it from Devices/Send.
func refreshNixplayPlaylists(ctx context.Context, state *nixplayBridgeState, client *bridgehub.Client) {
	playlists, err := state.nx.ListPlaylists(ctx)
	if err != nil {
		log.Printf("nixplay-bridge: list playlists: %v", err)
		return
	}
	devs := make([]protocol.BridgeDevice, 0, len(playlists))
	hiddenCount := 0
	for _, p := range playlists {
		if state.hidden != nil && state.hidden.IsHidden(p.ID) {
			hiddenCount++
			continue
		}
		devs = append(devs, protocol.BridgeDevice{
			ID:        p.ID,
			Type:      protocol.KindNixplay,
			Name:      p.Name,
			Connected: true,
			LastSeen:  time.Now().UTC(),
		})
	}
	if client != nil {
		if err := client.PublishDevices(devs); err != nil {
			log.Printf("nixplay-bridge: publish devices: %v", err)
		}
	}
	log.Printf("nixplay-bridge: %d gallery(s) (%d hidden)", len(devs), hiddenCount)
}

func handleNixplayBridgeCommand(ctx context.Context, state *nixplayBridgeState, client *bridgehub.Client, cmd protocol.CmdPayload) {
	switch cmd.Cmd {
	case protocol.CmdDiscover:
		refreshNixplayPlaylists(ctx, state, client)
	case protocol.CmdSendImage:
		var body protocol.SendImageBody
		if err := json.Unmarshal(cmd.Body, &body); err != nil {
			log.Printf("nixplay-bridge send.image: bad body: %v", err)
			return
		}
		err := deliverNixplayImage(ctx, state, body, cmd.DeviceID)
		if client != nil && body.SendID != "" {
			complete := protocol.SendCompletePayload{
				SendID:   body.SendID,
				DeviceID: cmd.DeviceID,
				Success:  err == nil,
			}
			if err != nil {
				complete.Detail = err.Error()
			}
			if pubErr := client.PublishSendComplete(complete); pubErr != nil {
				log.Printf("nixplay-bridge send.complete: %v", pubErr)
			}
		}
		if err != nil {
			log.Printf("nixplay-bridge send.image: %v", err)
		}
	default:
		log.Printf("nixplay-bridge: unhandled cmd %q", cmd.Cmd)
	}
}

func deliverNixplayImage(ctx context.Context, state *nixplayBridgeState, body protocol.SendImageBody, playlistID string) error {
	uploadCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	meta, raw, err := fetchHubImage(uploadCtx, body.HubBaseURL, body.ImageID)
	if err != nil {
		return err
	}
	fileName := meta.Name
	if fileName == "" {
		fileName = body.ImageID + ".jpg"
	}
	jpegData, jpegName, err := ensureNixplayJPEG(raw, fileName)
	if err != nil {
		return fmt.Errorf("prepare image: %w", err)
	}
	return state.nx.UploadPhoto(uploadCtx, playlistID, jpegName, jpegData)
}

// ensureNixplayJPEG returns data as JPEG bytes, converting HEIC (or anything
// else decodeAnyImage understands) if needed. Nixplay's upload API requires
// fileType "image/jpeg" — album originals are sometimes HEIC (iPhone photos).
func ensureNixplayJPEG(data []byte, fileName string) ([]byte, string, error) {
	ext := strings.ToLower(filepath.Ext(fileName))
	if ext == ".jpg" || ext == ".jpeg" {
		return data, fileName, nil
	}
	img, err := decodeAnyImage(data)
	if err != nil {
		return nil, "", fmt.Errorf("decode %s: %w", fileName, err)
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
		return nil, "", fmt.Errorf("encode jpeg: %w", err)
	}
	newName := strings.TrimSuffix(fileName, filepath.Ext(fileName)) + ".jpg"
	return buf.Bytes(), newName, nil
}
