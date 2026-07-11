# Nixplay Frame Sync Protocol (Socket.IO)

Device under study: Nixplay W15C-03 (Rockchip, Android 7.1.2, package `com.kitesystems.nix.frame`).
Sources: static analysis of jadx-decompiled `NixUI.apk` (`decompiled/sources/com/kitesystems/nix/nixplay/`)
cross-checked against a live mitmproxy capture (reverse-proxied against the real backend, frame's own
CA trust store patched to trust our cert). Live-confirmed items are marked **[LIVE]**; everything else
is **[STATIC]** (read from decompiled code, not observed on the wire).

## 1. Connection

- **Endpoint**: `wss://sync-v2.prod.nixplay.com` — hardcoded in `assets/prod/nix.properties` (`sync_url`),
  not fetched from remote config. **[STATIC+LIVE]**
- **Library**: `socket.io-client`, Engine.IO protocol version 3 (`EIO=3` in the query string). **[LIVE]**
- **Transport negotiation**: starts on HTTP long-polling (`GET`/`POST .../socket.io/?EIO=3&...`), then
  upgrades to WebSocket via the standard Engine.IO probe handshake (`2probe` → `3probe` → client sends
  bare `5` to confirm the upgrade). The very first app-level message (`syncReady`) is sent over the
  polling transport, before the WebSocket upgrade completes. **[LIVE]**
- `NixplayWebSocketHandler.initSocket()` sets `IO.Options.forceNew = true` and uses socket.io-client's
  default reconnection behavior. **[STATIC]**, class `com.kitesystems.nix.nixplay.NixplayWebSocketHandler`.
- **TLS**: no certificate pinning found (`CertificatePinner` is imported in `RetrofitClient.java` but
  never invoked with `.pin(...)`). Standard system-trust-store validation. Confirmed live: the frame
  accepted our connection once our CA was installed in `/system/etc/security/cacerts/`. **[STATIC+LIVE]**
- **DNS**: frame uses the DHCP-offered resolver (`net.dns1` = router IP), no hardcoded public resolver,
  no DNS-over-HTTPS. A router-level (dnsmasq) or on-device `/etc/hosts` override is sufficient to redirect
  this hostname. **[LIVE]** (checked via `getprop`/`resolv.conf` on-device)

### Other hostnames from `nix.properties` (not explored beyond noting they exist)

| key | value | notes |
|---|---|---|
| `sync_url` | `https://sync-v2.prod.nixplay.com` | the socket described in this doc |
| `pair_url` | `https://frm-ws-pair.nixplay.com` | separate pairing socket, not explored — frame was already paired |
| `frame_api_url` | `https://frame-api.prod.nixplay.com/v1/` | REST API, not explored |
| `update_url` | `https://update.nixplay.com/` | Softwinner/Rockchip OTA check-in, see `docs/ota.md`-equivalent notes below |
| `signage_update_url` | `http://update.nixplaysignage.com/` | plaintext HTTP — only used if `NixApp.isSignage()`, not applicable to this device |
| `linkcheck_url` | `https://check.nixplay.com/` | not explored |
| `proxy_url` | `https://s3-proxy.prod.nixplay.com` | not explored |
| `slideshow_bucket_name` | `nixplay-prod-slideshow` | S3 bucket seen in `syncSlideshowChange`/`syncSlideshowListChange`/`syncFramePlaylistConfigChange` URLs |
| `upload_bucket_name` | `recovery-np-bucket-prod` | not explored |
| `sync_interval_second` | `30` | config value, purpose not confirmed (possibly a fallback poll interval if the socket is down) |

### Query-string handshake parameters **[LIVE]**

```
EIO=3&rebootCount=1&sid=<engine.io session id>&frameId=<frame id>
&signature=<base64 HMAC>&count=<attempt count>&reason=<trigger reason, e.g. "ConnectivityChangedReceiver">
&uid=<8-char random per-attempt id>&timestamp=<ms epoch>&timedOut=false&transport=websocket
```

`signature` here is a *connection-level* digest, separate from the per-message `authDigest` described below.

## 2. Authentication

Everything is symmetric HMAC-SHA256 keyed by the frame's **`sync_key`** — a per-frame secret pulled once
from `/data/data/com.kitesystems.nix.frame/files/sync_key.props` on a rooted device (hex-encoded, 32 raw
bytes after decoding). There is no server-side-only secret involved anywhere in this scheme: possession of
`sync_key` is sufficient to forge fully valid traffic to/from this specific frame.

`AuthDigest.digest(byte[] key, byte[] msg)` — `com.kitesystems.nix.nixplay.AuthDigest`:
```
Mac.getInstance("HmacSHA256")
SecretKeySpec(key, "HmacSHA256")
digest = HMAC(key=sync_key_bytes, msg)
base64-encode(digest)   // Base64.encodeToString(..., Base64.NO_WRAP)
```

### Per-message digest — `Message.digest()` / `Message.verify()`, `com.kitesystems.nix.nixplay.Message`

```
msg = msgType + msgTime + msgData.digest()      // string concatenation, msgTime as decimal string
authDigest = base64(HMAC-SHA256(sync_key, msg))
```
Every `Data` subclass (one per `msgType`) implements its own `digest()` — see §4 for each type's formula.
Messages that fail `verify()` are **silently dropped** (`NixplayService.NixplayServiceHandler.handleMessage`
just `return`s). **[STATIC]**

Exception: `echoRequest`/`echoResponse` (heartbeat) carry **no `authKeyId`/`authDigest` at all** — confirmed
live, the heartbeat channel is unauthenticated. **[LIVE]**

## 3. Wire envelope

Socket.IO packet: `42["message","<json-string>"]`

- `4` = Engine.IO "message" packet
- `2` = Socket.IO "event" packet
- `"message"` = the (single, constant) Socket.IO event name used for every app-level message
- the payload is a **JSON-encoded string**, i.e. double-encoded — the array's second element is itself a
  JSON document serialized to a string, not a nested object.

Envelope fields (`Message.encode()`/`decode()`):
```json
{
  "msgType": "<see Message.Type enum, §4>",
  "msgTime": "<ms epoch, as a decimal string>",
  "msgData": { /* type-specific, see §4 */ },
  "authKeyId": "<frameId>",       // omitted when unauthenticated (e.g. echo)
  "authDigest": "<base64 HMAC>"   // omitted when unauthenticated
}
```

Transport-level Engine.IO ping/pong (bare `2` / `3`, no Socket.IO wrapper) also appears interleaved —
this is Engine.IO's own built-in keepalive, distinct from the app-level `echoRequest`/`echoResponse`
heartbeat described below. Both were observed live, running concurrently. **[LIVE]**

## 4. Message catalog

Full enum from `Message.Type` (`com.kitesystems.nix.nixplay.Message`), with `msgData` shape and `digest()`
formula for each, from the corresponding `com.kitesystems.nix.nixplay.messagedata.*` / `...pairing.*` /
`...pushing.*` class. **[LIVE]** tag = observed on the wire in our capture; otherwise **[STATIC]**.

### Sync / photo flow

| msgType | direction | msgData fields | digest() | status |
|---|---|---|---|---|
| `syncReady` | C→S | `{}` (empty) | `""` | **[LIVE]** — first message sent after connect, over the polling transport |
| `frameSpaceUpdate` | C→S | `spaceFree`, `spaceTotal` (bytes, longs) | `spaceFree + spaceTotal` | **[LIVE]** — `spaceTotal` was `16777216` (16 MiB local photo cache budget) |
| `syncSlideshowChange` | S→C | `slideshowUrl` (S3 JSON manifest URL) | `slideshowUrl` | **[LIVE]** — triggers the frame to (re)fetch its slideshow assignment |
| `syncSlideshowListChange` | S→C | `masterConfigUrl` (S3, `<frameId>_master_config.json`), optional `triggeredBy` | `masterConfigUrl` | **[LIVE]** |
| `syncFramePlaylistConfigChange` | S→C | `url` (S3, `<accountId>_playlist_config_<frameId>.json`) — note: encode class is `SyncPlaylistConfigChange`, JSON key is `"url"` not `"playlistConfigUrl"` | `url` | **[LIVE]** |
| `syncFriendDataChange` | S→C | `{}` (empty) | `""` | **[LIVE]** — bare signal, no payload |
| `syncSocialDataChangeToFrame` | S→C | not captured | — | **[STATIC]** only |
| `syncSettingsChange` | S→C | not captured | — | **[STATIC]** only |
| `syncCampaignChange` | S→C | not captured | — | **[STATIC]** only |
| `syncWidgetChange` | S→C | not captured | — | **[STATIC]** only |
| `processPhoto` | **C→S** | `photoMD5`, `frameModel` (e.g. `"W15C-03"`), `originalPhotoS3Key` (e.g. `"1622062/1622062_<md5>.jpg"`), optional `hint`, optional `gifting` (bool), `allowPublicAccess` (bool, only emitted if true) | `photoMD5 + frameModel + originalPhotoS3Key` (+ `gifting` if true) | **[LIVE]** — **the frame requests processing/download of a photo it has learned about; decode() is unimplemented, this type is client-originated only** |
| `processVideo` | C→S | analogous to `processPhoto` for video | — | **[STATIC]** only |
| `getPhoto` | **S→C** | `photoMD5`, `processedPhotoUrl` (presigned S3 URL to a frame-resolution JPEG, e.g. `nixplay-prod-processed.s3.us-west-2.amazonaws.com/output/W15C-03_<md5>.jpg?AWSAccessKeyId=...&Expires=...&Signature=...`), optional `hint` | `photoMD5 + processedPhotoUrl` | **[LIVE]** — encode() is unimplemented, this type is server-originated only; the frame plain-HTTPS-GETs `processedPhotoUrl` and caches the bytes at `files/photos/<photoMD5>` (no extension) |
| `getVideo` | S→C | analogous to `getPhoto` for video | — | **[STATIC]** only |
| `frameSpaceUpdate` | (see above) | | | |
| `frameStateChange` | ? | `name`, `value` (`Data.STATE_NAME`/`STATE_VALUE` constants exist, class not separately inspected) | — | **[STATIC]** only |

### Remote control / commands

| msgType | direction | msgData fields | digest() | status |
|---|---|---|---|---|
| `frameCommand` | S→C | `category` (`"remoteControl"` \| `"carousel"` \| `"system"` — constants `FrameCommand.CATEGORY_*`), `params`: array of `{key, value}` | `category + key` (first param's key only) | **[LIVE]** — observed `{"category":"remoteControl","params":[{"key":"button","value":"right"}]}`, i.e. simulated remote-control button presses. `carousel`/`system` categories not observed, names only. |
| `pushSoftwareUpdate` | S→C | class `com.kitesystems.nix.nixplay.pushing.PushSoftwareUpdate`, not inspected | — | **[STATIC]** only |
| `unpair` | S→C | class `com.kitesystems.nix.nixplay.pairing.Unpair`, not inspected | — | **[STATIC]** only |

### Heartbeat (unauthenticated)

| msgType | direction | msgData fields | status |
|---|---|---|---|
| `echoRequest` | C→S | `echoSequence` (string int, monotonic), `echoMessage` (literal `"♥♥♥"`) | **[LIVE]** — fired every **15.0s** (measured: 14951, 15005, 15004, 15003... ms between sends), no `authKeyId`/`authDigest` |
| `echoResponse` | S→C | mirrors the request: same `echoSequence`/`echoMessage`, echoed back within ~0ms | **[LIVE]** |

Driven by a separate `HeartbeatMonitor`, not the main message-dispatch switch. **[STATIC]**

### Pairing sub-protocol (not explored — device was already paired)

`pairRequest`, `pairResponse`, `pairReject`, `pairReady`, `pairChallenge`, `pairAcknowledge`,
`serverPairAck`, `rePairRequest`, `rePairResponse`, `b2bPairAcknowledge`,
`showPairingSerialPage`, `showSubscriptionPageRequest`, `showSubscriptionPageResponse`.
Classes live under `com.kitesystems.nix.nixplay.pairing.*`. This likely goes over the separate
`pair_url` (`frm-ws-pair.nixplay.com`) host, not `sync_url` — unconfirmed. **[STATIC]** only, entirely
unexplored.

## 5. Confirmed live sequence: pushing a new photo to the frame

Captured end-to-end by uploading photos to two Nixplay albums from the mobile app while the frame's
connection was redirected through mitmproxy (reverse-proxied to the real backend, frame's CA store
patched to trust our cert).

1. Someone adds a photo to an album synced to this frame (via mobile/web app — not observed, happens
   outside the socket).
2. Server → frame: `syncSlideshowChange` with a `slideshowUrl` pointing to a JSON manifest on
   `nixplay-prod-slideshow.s3.amazonaws.com`. (The frame's subsequent fetch of that manifest happens over
   plain HTTPS outside the socket connection — not captured.)
3. For each photo the frame determines it needs, frame → server: `processPhoto` naming the photo by MD5
   and its original S3 key.
4. Server → frame (near-instantly, ~1s): `getPhoto` with a presigned, frame-model-specific
   `processedPhotoUrl`.
5. Frame plain-HTTPS-GETs `processedPhotoUrl`, caches the JPEG at `files/photos/<photoMD5>`, and (based on
   `NixDatabase.db` schema — see below) inserts/updates a `photo` table row keyed by `lookup = photoMD5`.
   The exact download→DB-insert code path (`NixTask`/`ContentManager`, enqueued via
   `NixApp.getMediaItemTaskQueue()`) was not traced past the enqueue call.
6. Frame → server: `frameSpaceUpdate` reflecting the reduced `spaceFree`.
7. For a multi-photo batch (4 photos across 2 albums observed), steps 3–4 repeat as tight
   `processPhoto`/`getPhoto` pairs roughly 1–3s apart; `syncSlideshowListChange` +
   `syncFramePlaylistConfigChange` + `syncFriendDataChange` also fired together shortly after, apparently
   as a broader resync signal — relationship between this trio and the single-photo flow above is not
   fully confirmed (could be periodic, could be triggered by the same upload event).

**Important design implication**: the frame is the party that *requests* photo delivery (`processPhoto`)
after learning a photo exists via `syncSlideshowChange` → manifest fetch. The server does not unilaterally
push `getPhoto` out of nowhere — `getPhoto` is always a reply to a `processPhoto` request in what we
observed. A from-scratch server impersonation therefore needs to implement the manifest-URL step too, not
just answer `processPhoto` with a hardcoded `getPhoto`.

## 6. Local storage side effects (device-side, not on the wire)

- `files/photos/<photoMD5>`: raw JPEG bytes, no file extension, filename *is* the MD5 (no extra hashing
  step — matches `photo.lookup` in the DB directly).
- `databases/NixDatabase.db`, table `photo`: columns `_id, type, lookup, rotation, deleted,
  date_deleted, timestamp, caption, sender_name, sender_email, source, date_created, count_played,
  original_date_created, has_new_likes`. `lookup` is the join key to the cache filename. A `picture` table
  also exists in the schema but was empty (0 rows) on this device — appears to be a legacy/dead table.
- Observed cache budget: `spaceTotal = 16777216` bytes (16 MiB) for the whole local photo cache.

### How the app notices a new photo — **[STATIC, UNTESTED]**

Relevant for any companion process that wants to inject a photo locally (e.g. an MQTT-driven daemon
writing directly into `files/photos/` + `NixDatabase.db` instead of speaking the Socket.IO protocol).
Traced statically only — none of this has been verified live.

- There is a `ContentProvider`, `com.kitesystems.nix.frame.database.NixContentProvider`
  (`AndroidManifest.xml`, authority `nix`, exported), covering `content://nix/photo`
  (`PhotoDescriptor.PATH()`/`NAME()` = `"photo"`). Its `insert()`/`update()`/`delete()`/`bulkInsert()`
  call `getContentResolver().notifyChange(uri, null)` — but **only for writes that go through this
  provider**, not for a raw external process opening `NixDatabase.db` directly with its own SQLite
  connection.
- No class anywhere in the decompiled tree calls `registerContentObserver` on that URI (or anything
  under `com.kitesystems.nix.*`) — so even a proper provider-based insert has no registered listener to
  actually react to it. This provider looks vestigial for live UI updates.
- The real display path is a static in-memory singleton: `CarouselDO.instance().getCarousel()`, populated
  via a plain `setCarousel(Carousel)` setter — not a live `Cursor`/DB query. `ContentManager.getContent()`
  (`ContentManager.java`) reads from this singleton. `ContentManager` itself is an `IntentService`
  (`onHandleIntent`) — it only runs when explicitly started with an `Intent`, no polling timer of its own.
- **Conclusion: a raw SQLite insert + cache file write alone is expected to be invisible to the running
  app** — something upstream (not traced; likely inside the `getPhoto`-handling code path in
  `NixplayService` or a specific `ContentManager` intent action) rebuilds the `Carousel` object and calls
  `setCarousel()` after a legitimate sync, and that call is the actual trigger, not the DB row itself.
- **Not yet tried**: (a) inserting via the real provider (`adb shell content insert --uri content://nix/photo ...`)
  plus dropping the cache file, to see if anything picks it up despite no observer being registered
  (e.g. if `ContentManager` polls `content://nix/photo` on its own intent-triggered runs); (b) the
  blunter fallback of having a companion daemon restart the app process (or specifically
  `ContentManager`/`NixplayService`) after writing the file + DB row, forcing a cold re-read from disk —
  more heavy-handed (visible reload) but doesn't depend on reverse-engineering an internal signal that
  could silently change between app versions.

## 7. Reconnection behavior **[LIVE]**

On any disconnect (including `ABNORMAL_CLOSURE`, seen repeatedly while we were bouncing wifi/mitmproxy
during testing), socket.io-client reconnects automatically (`forceNew=true`). Each new connection attempt:
- generates a fresh `uid` (8 random chars) and `timestamp`,
- recomputes `signature` for the new timestamp,
- increments `count`,
- carries a `reason` describing the trigger (observed: `"ConnectivityChangedReceiver"`),
- re-sends `syncReady` as the first app-level message, again over polling before the WS upgrade.

## 8. Known unknowns / not yet captured

- Exact contents/schema of the `slideshowUrl` / `masterConfigUrl` / playlist-config JSON manifests — never
  fetched or inspected ourselves; we only saw the URLs pass by on the socket.
- The `NixTask`/`ContentManager` code path from `getPhoto` receipt to file-write + DB-insert — confirmed
  only that it's enqueued (`NixApp.getMediaItemTaskQueue().enqueue(...)`), not traced further.
- `pushSoftwareUpdate`, `unpair` — message shapes not inspected (classes not opened).
- `frameCommand` categories `carousel` and `system` — names known, payloads unconfirmed.
- The entire pairing sub-protocol and whether it runs over `sync_url` or the separate `pair_url`
  (`frm-ws-pair.nixplay.com`) host.
- Whether `sync_interval_second=30` (from `nix.properties`) does anything observable — no periodic
  30-second behavior was noticed in ~15 minutes of live capture beyond the 15s echo heartbeat.
- TLS handshake has only been tested against mitmproxy's Python/OpenSSL stack terminating the connection.
  Whether the frame's OkHttp/socket.io TLS client is picky about anything (SNI, ALPN, cipher suite
  selection, TLS version) beyond basic cert-chain validation is unverified — worth checking once a Go
  `net/http`-based listener (the real joyous-hub adapter) is standing in instead of mitmproxy.

## 9. Onboarding / interception prerequisites (for reference, see main conversation for full reasoning)

To stand in for the real Nixplay backend for a given frame, you need:
1. That frame's `sync_key` (root-pull `/data/data/com.kitesystems.nix.frame/files/sync_key.props` once).
2. A CA certificate the frame trusts, installed into `/system/etc/security/cacerts/` (filename = OpenSSL
   `X509_NAME_hash_old`-style hex hash + `.0`, e.g. `c8750f0d.0` for a mitmproxy-generated CA) — this is a
   one-time root/adb operation per frame and is the only real barrier; there's no way around it without a
   publicly-trusted cert for a hostname you don't own.
3. DNS control for `sync-v2.prod.nixplay.com` (router-level dnsmasq override is sufficient and doesn't
   require ongoing device access) redirecting to your own server.

After steps 1–3, all further interaction is exactly the protocol documented above.

### Also redirect `update.nixplay.com`

Once the CA cert + hosts override are installed on `/system`, they'd be wiped out by a real OTA flash
(OTA rewrites the `/system` partition). Redirecting `update_url` (`https://update.nixplay.com/`, see the
hostname table in §1) at the same target — even before any real replacement OTA-check service exists —
blocks this: the frame's `CheckingTask.sendPost()` (`com.softwinner.update.CheckingTask`) just fails to
connect and the app treats that as "no update found" (`mErrorCode = 3`), which is a clean, safe no-op, not
a crash. This is a cheap way to protect already-applied local changes from being silently reverted by a
legitimate firmware push. On this device, both `sync-v2.prod.nixplay.com` and `update.nixplay.com` are
currently pointed at `m1ni` (192.168.51.7) in `/etc/hosts`, ahead of the real joyous-hub adapter existing.
