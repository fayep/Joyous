# Nixplay mobile app REST API (photo upload)

Source: static analysis of the Hermes JS bundle inside `Nixplay+App_3.73.2_APKPure.xapk`
(`com.creedon.Nixplay.apk`, package `com.creedon.Nixplay`, a React Native app — distinct from the
frame-side `NixUI.apk`/`com.kitesystems.nix.frame` covered in `socketio_protocol.md`). The Java/Kotlin
side is mostly RN scaffolding; the actual API logic lives in a compiled Hermes bytecode bundle
(`assets/index.android.bundle`, bytecode version 96). Decompiled to pseudo-JS with
[`hermes-dec`](https://pypi.org/project/hermes-dec/)'s `hbc-decompiler` (`uv venv` + `uv pip install
hermes-dec` into a throwaway venv).

**This has now been confirmed end-to-end against a real account** (joyous-hub's `nixplay-bridge`,
2026-07-11/12 — see `joyous-hub/nixplaybridge/`). The static analysis got the endpoint names and overall
shape right but got several field-level details wrong; those are corrected below and marked **[LIVE]**.
Anything still marked **[STATIC]** has not been exercised.

## Base URL

`https://mobile-api.nixplay.com` — from `BuildConfig.MOBILE_API_URL` in the decompiled Java
(`sources/com/creedon/Nixplay/BuildConfig.java`). Distinct from `www.nixplay.com` (web/shop) and
`gateway.nixplay.com` (`COMPETITION_API_URL`, unrelated). **[LIVE]**

### WAF blocks default HTTP client User-Agents **[LIVE]**

Every endpoint below 403s with a bare Apache-style HTML error page (not a JSON API error) if the
request's `User-Agent` looks like a bot/library default — confirmed for both curl's own default and Go's
`Go-http-client/1.1`. A browser UA or `okhttp/4.9.3` (the app's own HTTP library, per decompiled
`RetrofitClient.java`) both get past the WAF and reach the real API (which then legitimately 401s without
auth). Send a non-default `User-Agent` on every request — `nixplay-bridge` uses `okhttp/4.9.3`.

## Authentication

`POST /v1/auth/signin` — **[LIVE]**, works exactly as decompiled.

Body:
```json
{
  "username": "<email>",
  "password": "<password>",
  "deviceId": "<device id>",
  "notificationKey": "<push token, empty string is fine>",
  "platform": "android",
  "model": "<device model>",
  "version": "<app version, e.g. 3.73.2>",
  "env": "prod"
}
```

Response: `{ "token": "<JWT>", ... }`. The app decodes the JWT client-side afterward to pull
`username`/`email`/`firstName`/`lastName`/`userProfileId` claims — i.e. the JWT payload carries user
profile data, not just an opaque session id. **[STATIC]** for the claim-decoding part; token itself is
**[LIVE]**-confirmed to work as `Authorization: Bearer <token>` on every other endpoint.

`/v1/auth/renew-token` and `/v1/tokeninfo` exist (names only, not exercised) — **[STATIC]**.

## Listing galleries ("playlists")

Nixplay's own term for a photo collection assigned to frames is **playlist**, not "album" (the app's
"album" terminology/`/v1/albums` path is reused for the *Google Photos import* feature and hits a
different base URL — not Nixplay's own gallery list, a dead end, don't reuse it).

`GET /v6/playlists/` with `Authorization: Bearer <token>` → **[LIVE]** — returns a bare JSON array (not
wrapped in `{"playlists": [...]}`) of playlist objects. Confirmed fields include (there are more):
```json
{
  "id": 3598701,
  "name": "Pets",
  "picture_count": 23,
  "on_frames": [{"pk": 1074670, "name": "", "serial_number": "9508993999462830"}],
  "type": "normal",
  "last_updated_date": "2026-07-12T03:47:39.000Z",
  "created_date": "2020-11-11T21:58:31.000Z"
}
```
`id` is a **JSON number**, not a string — matters for the upload flow below.

`POST /v3/playlists` `{"name": "<name>"}` → creates a playlist (**[STATIC]**, not exercised).

## Uploading a photo to a playlist — **[LIVE]**, confirmed end-to-end

Real accepted flow, corrected from the decompiled version (which got the field names wrong in two
places — noted inline):

### 1. Open an upload batch

`POST /v2/photos/receivers` (`Authorization: Bearer <token>`, `Content-Type: application/json`)

```json
{
  "playlistIds": [3598701],
  "friends": [],
  "total": 1,
  "camera": true
}
```
**Correction:** the decompiled code suggested a top-level `"albumId": <playlistId>` field. That field
exists but is a *different* concept — sending a playlist id there fails with
`{"code":400,"message":"Invalid parameter: albumId","name":"InvalidParameterError"}`. The playlist id
goes in the `playlistIds` array (as a **number**, not a string — sending it as a string previously failed
with `{"code":400,"message":"albumId (number) is required","name":"AssertionError"}` before we even got
to the "wrong field" error).

Response: `{"token": "<uploadToken>", "trackerIds": [{"trackerId": "...", "receiver": "3598701", "type":
"playlist"}]}`. Only `token` is needed going forward — reuse it for every file in the batch and for the
finalize call.

### 2. Get a presigned S3 upload policy per file

`GET /v1/photos/S3token` (video variant: `/v1/videos/S3token`, not exercised), `Authorization: Bearer
<token>`, query params:

```
uploadToken=<from step 1>
fileName=<name>
fileType=image/jpeg
fileSize=<byte length>
```
**Correction:** `fileType` and `fileSize` are both required — omitting either 400s
(`"fileType (string) is required"`, then `"fileSize (string) is required"`). The decompiled code didn't
show where these came from.

Response — **correction:** nested under a `data` key, and there is no `s3UploadFields` map at all (the
decompiled code's field name was wrong):
```json
{
  "data": {
    "acl": "authenticated-read",
    "key": "<account>/<hash>.upload",
    "AWSAccessKeyId": "AKIA...",
    "Policy": "<base64 S3 POST policy>",
    "Signature": "<base64 sig>",
    "userUploadId": "<hash>",
    "batchUploadId": "<hash, same as userUploadId>",
    "userUploadIds": ["<different hash>"],
    "fileType": "image/jpeg",
    "fileSize": 287,
    "s3UploadUrl": "https://nixplay-prod-upload.s3.amazonaws.com"
  },
  "storageInfo": {"contentType": "PHOTO", "storageAvailable": 273109835, "storageLimit": 524288000, "storageUsed": 251054709}
}
```

### 3. POST directly to S3 — form fields, in order

The response above doesn't literally name `Content-Type`, `success_action_status`, or
`x-amz-meta-batch-upload-id` as fields, but the base64-decoded `Policy` document's `conditions` require
them (`["starts-with","$Content-Type","image/jpeg"]`, `{"success_action_status":"201"}`,
`{"x-amz-meta-batch-upload-id":"<batchUploadId>"}`). Confirmed working field set, `key` first and `file`
last:

```
key = data.key
acl = data.acl
AWSAccessKeyId = data.AWSAccessKeyId
Policy = data.Policy
Signature = data.Signature
Content-Type = image/jpeg
success_action_status = 201
x-amz-meta-batch-upload-id = data.batchUploadId
file = <raw bytes>
```
S3 replies `201 Created` on success. No Nixplay auth involved; this hits AWS directly.

### 4. Finalize the batch

`POST /v3/photos/upload-completed` (`Authorization: Bearer <token>`) body `{"token": "<uploadToken from
step 1>"}` → `{"success": true}`.

**Note on visible confirmation:** the target playlist's `last_updated_date` (from `GET /v6/playlists/`)
ticks forward immediately on finalize; `picture_count` did *not* increment immediately in testing — it
appears Nixplay processes the upload asynchronously (matches `socketio_protocol.md`'s description of the
frame-side `processPhoto`/`getPhoto` exchange happening after a `syncSlideshowChange` signal, not
synchronously with upload). The photo does show up in the app/gallery within a short delay.

## Content type requirement

The API only accepts `image/jpeg` (confirmed via `fileType`) — HEIC or other formats must be converted to
JPEG client-side before upload. `nixplay-bridge` does this by decoding with the hub's existing
`decodeAnyImage` (which already handles HEIC via `goheif`, plus EXIF orientation) and re-encoding to JPEG
when the source filename isn't already `.jpg`/`.jpeg`.

## Known unknowns

- `/v1/auth/renew-token` and `/v1/tokeninfo` request/response shapes — endpoint names only, not
  exercised. `nixplay-bridge` currently just re-signs-in from scratch on JWT expiry rather than using
  these.
- Whether `/v2/photos/receivers` truly needs `total` to match the number of files uploaded in the batch,
  or whether it's advisory (only ever tested with `total: 1`).
- Video upload (`/v1/videos/S3token`) — not exercised, likely parallels the photo flow.
- Exact timing of the async processing between finalize and the photo becoming visible/counted.
