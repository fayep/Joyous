package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// MQTTPublisher is satisfied by the real broker and by test doubles.
type MQTTPublisher interface {
	Publish(topic string, payload []byte) error
}

// Hub wires together the broker, bridges, device registry, image store, and HTTP server.
type Hub struct {
	devices    *DeviceRegistry
	images     *ImageStore
	samsung    *SamsungStore
	publisher  MQTTPublisher
	serverAddr string // e.g. "192.168.1.5:8080" — used in play URLs
}

// handleDevices serves GET /api/devices.
func (h *Hub) handleDevices(w http.ResponseWriter, r *http.Request) {
	devs := h.devices.List()
	if devs == nil {
		devs = []Device{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(devs)
}

// handleImages serves GET /api/images.
func (h *Hub) handleImages(w http.ResponseWriter, r *http.Request) {
	imgs, _ := h.images.ListImages()
	if imgs == nil {
		imgs = []ImageMeta{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(imgs)
}

// handleImageUpload serves POST /api/images.
func (h *Hub) handleImageUpload(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(64 << 20); err != nil {
		http.Error(w, "parse form: "+err.Error(), http.StatusBadRequest)
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "file field required", http.StatusBadRequest)
		return
	}
	defer file.Close()

	name := header.Filename
	id, err := h.images.Store(file, name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"id": id, "name": name})
}

// handleImageDelete serves DELETE /api/images/{id}.
func (h *Hub) handleImageDelete(w http.ResponseWriter, r *http.Request, id string) {
	h.images.DeleteImage(id)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

// handleDisplay serves POST /api/devices/{id}/display.
func (h *Hub) handleDisplay(w http.ResponseWriter, r *http.Request, deviceID string) {
	var body struct {
		ImageID string `json:"image_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.ImageID == "" {
		http.Error(w, "image_id required", http.StatusBadRequest)
		return
	}
	if err := h.SendImageToDevice(deviceID, body.ImageID); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusAccepted)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

// handleRefresh serves POST /api/devices/{id}/refresh (InkJoy only).
func (h *Hub) handleRefresh(w http.ResponseWriter, r *http.Request, deviceID string) {
	dev, ok := h.devices.Get(deviceID)
	if !ok || dev.Type != DeviceTypeInkJoy {
		http.Error(w, "inkjoy device required", http.StatusBadRequest)
		return
	}
	payload := buildActionPayloadFor(dev.MAC, "image_refresh", nil)
	if err := h.publisher.Publish("/inkjoyap/"+dev.MAC, payload); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusAccepted)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

// handleSaveCrop serves POST /api/images/{id}/crop.
func (h *Hub) handleSaveCrop(w http.ResponseWriter, r *http.Request, id string) {
	var body struct {
		Format string  `json:"format"`
		X      float64 `json:"x"`
		Y      float64 `json:"y"`
		W      float64 `json:"w"`
		H      float64 `json:"h"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Format == "" {
		http.Error(w, "format, x, y, w, h required", http.StatusBadRequest)
		return
	}
	if body.W <= 0 || body.H <= 0 {
		http.Error(w, "w and h must be > 0", http.StatusBadRequest)
		return
	}
	rect := CropRect{X: body.X, Y: body.Y, W: body.W, H: body.H}
	if err := h.images.SetCrop(id, body.Format, rect); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

// handleDeleteCrop serves DELETE /api/images/{id}/crop?format=...
func (h *Hub) handleDeleteCrop(w http.ResponseWriter, r *http.Request, id string) {
	format := r.URL.Query().Get("format")
	if format == "" {
		http.Error(w, "format query param required", http.StatusBadRequest)
		return
	}
	if err := h.images.DeleteCrop(id, format); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

// handleStatic serves the embedded SPA for any non-API route.
func (h *Hub) handleStatic(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Write([]byte(indexHTML))
}

// ── MQTT payload helpers ─────────────────────────────────────────────────────

func buildActionPayloadFor(mac, action string, data map[string]any) []byte {
	msg := map[string]any{
		"action": action,
		"msgid":  fmt.Sprintf("%d", time.Now().UnixMilli()),
		"stamac": mac,
	}
	if data != nil {
		msg["data"] = data
	}
	b, _ := json.Marshal(msg)
	return b
}

func buildPlayPayload(mac, imgURL, serverAddr string) []byte {
	// Parse host and path from imgURL (simple split, no net/url to keep import lean).
	host := serverAddr
	path := ""
	if i := len("http://") + len(host); i < len(imgURL) {
		path = imgURL[i:]
	}
	// Port is embedded in host string (e.g. "192.168.1.5:8080").
	port := "8080"
	if j := len(host) - 1; j >= 0 {
		for k := j; k >= 0; k-- {
			if host[k] == ':' {
				port = host[k+1:]
				host = host[:k]
				break
			}
		}
	}
	return buildActionPayloadFor(mac, "play", map[string]any{
		"imgurl": imgURL,
		"host":   host,
		"port":   port,
		"path":   path,
	})
}

// ── Embedded static HTML ─────────────────────────────────────────────────────

const indexHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>Joyous</title>
<style>
  body{font-family:system-ui,sans-serif;margin:0;padding:0;background:#f5f5f5}
  header{background:#1a1a2e;color:#fff;padding:1rem 2rem;display:flex;align-items:center;gap:1rem}
  header h1{margin:0;font-size:1.2rem}
  nav button{background:none;border:none;color:#aaa;font-size:1rem;cursor:pointer;padding:.5rem 1rem}
  nav button.active{color:#fff;border-bottom:2px solid #fff}
  main{padding:2rem;max-width:1200px;margin:auto}
  .card{background:#fff;border-radius:8px;padding:1rem 1.5rem;margin-bottom:1rem;box-shadow:0 1px 3px #0002}
  .device-mac{font-family:monospace;font-weight:bold}
  .badge{display:inline-block;padding:2px 8px;border-radius:12px;font-size:.75rem}
  .badge.online{background:#d4edda;color:#155724}
  .badge.offline{background:#f8d7da;color:#721c24}
  .btn{padding:.4rem .9rem;border:none;border-radius:4px;cursor:pointer;font-size:.9rem}
  .btn-primary{background:#1a1a2e;color:#fff}
  .btn-sm{padding:.25rem .6rem;font-size:.8rem}
  .img-grid{display:grid;grid-template-columns:repeat(auto-fill,minmax(200px,1fr));gap:1rem}
  .img-card{background:#fff;border-radius:8px;overflow:hidden;box-shadow:0 1px 3px #0002;text-align:center}
  .img-card img{width:100%;aspect-ratio:4/3;object-fit:contain;display:block;background:#eee}
  .img-card .card-body{padding:.5rem .75rem}
  .img-card .name{font-size:.8rem;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;margin-bottom:.4rem}
  /* crop modal */
  #crop-modal{display:none;position:fixed;inset:0;background:#000c;z-index:1000;align-items:center;justify-content:center}
  #crop-modal.open{display:flex}
  #crop-ui{background:#1a1a2e;border-radius:10px;padding:1rem;display:flex;flex-direction:column;gap:.6rem;max-width:96vw;max-height:96vh}
  #crop-toolbar{display:flex;gap:.5rem;align-items:center;flex-wrap:wrap}
  #crop-toolbar select{padding:.3rem .5rem;border-radius:4px;border:1px solid #555;background:#2a2a4e;color:#fff;font-size:.85rem}
  #crop-toolbar label{color:#aaa;font-size:.8rem}
  #crop-stage{position:relative;flex:1;overflow:hidden;background:#111;border-radius:6px;min-width:min(80vw,600px);min-height:min(60vh,400px)}
  #crop-img{position:absolute;display:block;user-select:none;-webkit-user-drag:none}
  #crop-box{position:absolute;box-sizing:border-box;border:2px solid #fff;box-shadow:0 0 0 9999px rgba(0,0,0,.5);cursor:move;touch-action:none}
  .ch{position:absolute;width:14px;height:14px;background:#fff;border:2px solid #333;border-radius:2px}
  .ch[data-h=tl]{top:-7px;left:-7px;cursor:nwse-resize}
  .ch[data-h=tr]{top:-7px;right:-7px;cursor:nesw-resize}
  .ch[data-h=bl]{bottom:-7px;left:-7px;cursor:nesw-resize}
  .ch[data-h=br]{bottom:-7px;right:-7px;cursor:nwse-resize}
  #crop-hint{color:#888;font-size:.75rem;text-align:right}
  .crop-dim{position:absolute;box-sizing:border-box;pointer-events:none;border:1.5px dashed rgba(255,255,255,.38)}
  .crop-dim-label{position:absolute;top:3px;left:5px;font:10px/1 sans-serif;color:rgba(255,255,255,.5);pointer-events:none}
  #upload-zone{border:2px dashed #ccc;border-radius:8px;padding:2rem;text-align:center;margin-bottom:1rem;cursor:pointer}
  #upload-zone.drag{border-color:#1a1a2e;background:#f0f0ff}
  input[type=file]{display:none}
</style>
</head>
<body>
<header>
  <h1>Joyous</h1>
  <nav>
    <button class="active" onclick="showTab('devices',this)">Devices</button>
    <button onclick="showTab('album',this)">Album</button>
    <button onclick="showTab('samsung',this)">Samsung</button>
  </nav>
</header>
<main>
  <div id="tab-devices">
    <div style="margin-bottom:1rem">
      <button class="btn btn-primary btn-sm" id="discover-btn" onclick="discoverFrames()">Discover photo frames</button>
      <span id="discover-status" style="margin-left:.75rem;color:#666;font-size:.9rem"></span>
    </div>
    <div id="device-list"><p>Loading…</p></div>
  </div>
  <div id="tab-album" style="display:none">
    <div id="upload-zone" onclick="document.getElementById('file-input').click()">
      Drop images here or click to upload (.bin, .png, .jpg)
    </div>
    <input type="file" id="file-input" accept=".bin,.png,.jpg,.jpeg" multiple>
    <div id="image-grid" class="img-grid"></div>
  </div>
  <div id="tab-samsung" style="display:none">
    <div class="card">
      <p>Install URL for Samsung E-Paper app (Custom App player):</p>
      <code id="samsung-install-url">loading…</code>
      <p style="color:#666;font-size:.9rem;margin-top:.75rem">Place signed <code>joyous-widget.wgt</code> in <code>data/samsung/</code> on the hub.</p>
    </div>
    <div class="card">
      <label>Frame ID <input id="samsung-frame-id" placeholder="living-room" style="margin-left:.5rem;padding:.3rem"></label>
      <button class="btn btn-sm btn-primary" style="margin-left:.5rem" onclick="loadSamsungFrame()">Load</button>
    </div>
    <div id="samsung-editor" class="card" style="display:none">
      <h3 style="margin-top:0" id="samsung-frame-title"></h3>
      <div style="display:flex;flex-wrap:wrap;gap:1rem;align-items:end;margin-bottom:1rem">
        <label>Poll interval (min)<br><input type="number" id="samsung-poll" min="1" value="60" style="width:5rem;padding:.3rem"></label>
        <label>Inactive begin<br><input type="time" id="samsung-inactive-begin" style="padding:.3rem"></label>
        <label>Inactive end<br><input type="time" id="samsung-inactive-end" style="padding:.3rem"></label>
        <label>Crop format<br>
          <select id="samsung-crop-format" style="padding:.3rem">
            <option value="16:9">Landscape 16:9</option>
            <option value="9:16">Portrait 9:16</option>
            <option value="4:3">Landscape 4:3</option>
            <option value="3:4">Portrait 3:4</option>
            <option value="1:1">Square 1:1</option>
          </select>
        </label>
        <label>Width<br><input type="number" id="samsung-display-width" min="0" placeholder="2560" style="width:6rem;padding:.3rem"></label>
        <label>Height<br><input type="number" id="samsung-display-height" min="0" placeholder="1440" style="width:6rem;padding:.3rem"></label>
        <button class="btn btn-sm btn-primary" onclick="saveSamsungConfig()">Save config</button>
      </div>
      <div id="samsung-upload-zone" style="border:2px dashed #ccc;border-radius:8px;padding:1.5rem;text-align:center;cursor:pointer;margin-bottom:1rem" onclick="document.getElementById('samsung-file-input').click()">
        Drop image to upload for this frame
      </div>
      <input type="file" id="samsung-file-input" accept=".png,.jpg,.jpeg,.heic" style="display:none">
      <div id="samsung-status" style="font-size:.9rem;color:#666"></div>
      <img id="samsung-preview" style="max-width:100%;margin-top:1rem;display:none;border-radius:6px">
    </div>
    <div id="samsung-list"></div>
  </div>
</main>
<script>
let devices=[], images=[];

function showTab(name,btn){
  document.querySelectorAll('[id^=tab-]').forEach(e=>e.style.display='none');
  document.getElementById('tab-'+name).style.display='';
  document.querySelectorAll('nav button').forEach(b=>b.classList.remove('active'));
  btn.classList.add('active');
}

async function loadDevices(){
  const r=await fetch('/api/devices'); devices=await r.json();
  const el=document.getElementById('device-list');
  if(!devices||!devices.length){el.innerHTML='<p>No frames yet. Connect an InkJoy frame via MQTT, or click Discover for Samsung displays.</p>';return;}
  el.innerHTML=devices.map(d=>{
    const label=d.name||d.mac||d.ip||d.id;
    const type=d.type||'inkjoy';
    const status=d.connected?'<span class="badge online">online</span>':'<span class="badge offline">offline</span>';
    const meta=type==='inkjoy'
      ? ((d.firmware?'fw '+d.firmware+' ':'')+(d.battery?'🔋'+d.battery+'% ':'')+(d.rssi?'📶'+d.rssi+'dBm ':''))
      : (d.ip?d.ip+' ':'')+(d.display_crop_format?('<span style="color:#666">'+d.display_crop_format+(d.display_width?(' · '+d.display_width+'×'+d.display_height):'')+'</span> '):'')+(d.usn?'<span style="color:#888;font-size:.8rem">'+d.usn.split('::')[0]+'</span>':'');
    const refreshBtn=type==='inkjoy'?'<button class="btn btn-sm btn-primary" onclick="refreshDevice(\''+d.id+'\')">Refresh display</button> ':'';
    return '<div class="card">'+
      '<span class="badge" style="background:#eee;color:#333;margin-right:.5rem">'+type+'</span>'+
      '<strong>'+label+'</strong> '+status+' '+
      '<span style="margin-left:.5rem;color:#666;font-size:.9rem">'+meta+'</span>'+
      '<div style="margin-top:.5rem">'+refreshBtn+
      '<button class="btn btn-sm btn-primary" onclick="sendToFrame(\''+d.id+'\')">Send image</button>'+
      '</div></div>';
  }).join('');
}

async function discoverFrames(){
  const btn=document.getElementById('discover-btn');
  const st=document.getElementById('discover-status');
  btn.disabled=true; st.textContent='Scanning network…';
  try{
    const r=await fetch('/api/devices/discover',{method:'POST'});
    const data=await r.json();
    if(!r.ok) throw new Error(data.error||r.statusText);
    const seen=data.ssdp_seen!=null?' ('+data.ssdp_seen+' UPnP devices)':'';
    st.textContent=data.found?'Found '+data.found+' frame(s)'+seen:'No frames matched'+(seen||'');
    loadDevices();
  }catch(e){
    st.textContent='Discovery failed: '+e.message;
  }finally{
    btn.disabled=false;
  }
}

async function refreshDevice(id){
  await fetch('/api/devices/'+encodeURIComponent(id)+'/refresh',{method:'POST'});
}

let imageCropsCache = {}; // id → crops map, kept fresh by loadImages + save/delete

async function loadImages(){
  const r=await fetch('/api/images'); images=await r.json();
  images.forEach(img=>{ imageCropsCache[img.id]=img.crops||{}; });
  const el=document.getElementById('image-grid');
  if(!images||!images.length){el.innerHTML='<p>No images uploaded yet.</p>';return;}
  el.innerHTML=images.map(img=>'<div class="img-card" id="card-'+img.id+'">'+
    '<img src="/images/'+img.id+'/thumb?t='+Date.now()+'" alt="'+img.name+'" loading="lazy">'+
    '<div class="card-body">'+
      '<div class="name" title="'+img.name+'">'+img.name+'</div>'+
      '<button class="btn btn-sm btn-primary" onclick="openCrop(\''+img.id+'\')">Frame</button> '+
      '<button class="btn btn-sm btn-primary" onclick="sendImageToFrame(\''+img.id+'\')">Send</button> '+
      '<button class="btn btn-sm" style="background:#dc3545;color:#fff" onclick="deleteImg(\''+img.id+'\')">✕</button>'+
    '</div>'+
  '</div>').join('');
}

async function sendToFrame(deviceId){
  if(!devices.length){alert('No devices — discover or connect a frame first');return;}
  let imageId=prompt('Image id from album:\n'+(images.length?images.map(i=>i.id+' '+i.name).join('\n'):'(upload images first)'));
  if(!imageId)return;
  imageId=imageId.trim().split(/\s+/)[0];
  const r=await fetch('/api/devices/'+encodeURIComponent(deviceId)+'/display',{
    method:'POST',headers:{'Content-Type':'application/json'},
    body:JSON.stringify({image_id:imageId})
  });
  if(!r.ok)alert('Error: '+(await r.text()));
}

async function sendImageToFrame(imageId){
  if(!devices.length){alert('No devices — discover or connect a frame first');return;}
  let deviceId=devices.length===1?devices[0].id:prompt('Device id:\n'+devices.map(d=>d.id+' — '+(d.name||d.mac||d.ip)).join('\n'));
  if(!deviceId)return;
  deviceId=deviceId.trim().split(/\s+/)[0];
  const r=await fetch('/api/devices/'+encodeURIComponent(deviceId)+'/display',{
    method:'POST',headers:{'Content-Type':'application/json'},
    body:JSON.stringify({image_id:imageId})
  });
  if(!r.ok)alert('Error: '+(await r.text()));
}

async function deleteImg(id){
  if(!confirm('Delete image?'))return;
  await fetch('/api/images/'+id,{method:'DELETE'});
  loadImages();
}

document.getElementById('file-input').addEventListener('change',async e=>{
  for(const f of e.target.files){
    const fd=new FormData(); fd.append('file',f);
    await fetch('/api/images',{method:'POST',body:fd});
  }
  loadImages();
});

const zone=document.getElementById('upload-zone');
zone.addEventListener('dragover',e=>{e.preventDefault();zone.classList.add('drag')});
zone.addEventListener('dragleave',()=>zone.classList.remove('drag'));
zone.addEventListener('drop',async e=>{
  e.preventDefault();zone.classList.remove('drag');
  for(const f of e.dataTransfer.files){
    const fd=new FormData();fd.append('file',f);
    await fetch('/api/images',{method:'POST',body:fd});
  }
  loadImages();
});

loadDevices(); loadImages();
setInterval(loadDevices,5000);
document.getElementById('samsung-install-url').textContent=location.origin+'/samsung/';
loadSamsungList();

async function loadSamsungList(){
  const r=await fetch('/api/samsung'); const frames=await r.json();
  const el=document.getElementById('samsung-list');
  if(!frames.length){el.innerHTML='<p style="color:#666">No Samsung frames yet — enter a frame ID above.</p>';return;}
  el.innerHTML='<div class="card"><strong>Known frames</strong><ul>'+
    frames.map(f=>'<li><a href="#" onclick="document.getElementById(\'samsung-frame-id\').value=\''+f.id+'\';loadSamsungFrame();return false">'+f.id+'</a> '+
    (f.has_image?'🖼':'no image')+' '+(f.locked?'🔒':'')+' poll '+f.poll_interval_minutes+'m'+
    (f.inactive_begin?' sleep '+f.inactive_begin+'-'+f.inactive_end:'')+'</li>').join('')+
    '</ul></div>';
}

let samsungCurrentId=null;
async function loadSamsungFrame(){
  const id=document.getElementById('samsung-frame-id').value.trim();
  if(!id)return;
  samsungCurrentId=id;
  document.getElementById('samsung-editor').style.display='';
  document.getElementById('samsung-frame-title').textContent='Frame: '+id;
  const r=await fetch('/samsung/'+encodeURIComponent(id)+'/status');
  const s=await r.json();
  document.getElementById('samsung-poll').value=s.poll_interval_minutes||60;
  document.getElementById('samsung-inactive-begin').value=s.inactive_begin||'';
  document.getElementById('samsung-inactive-end').value=s.inactive_end||'';
  document.getElementById('samsung-crop-format').value=s.crop_format||'16:9';
  document.getElementById('samsung-display-width').value=s.display_width||'';
  document.getElementById('samsung-display-height').value=s.display_height||'';
  const st=document.getElementById('samsung-status');
  st.textContent=(s.has_image?'Image etag '+s.etag:'No image yet')+(s.locked?' (locked)':'');
  if(s.crop_format||s.display_width){
    st.textContent+=' · display '+(s.crop_format||'16:9')+(s.display_width?(' '+s.display_width+'×'+s.display_height):'');
  }
  const prev=document.getElementById('samsung-preview');
  if(s.has_image&&!s.locked){prev.style.display='';prev.src='/samsung/'+encodeURIComponent(id)+'.png?t='+Date.now();}
  else{prev.style.display='none';}
  loadSamsungList();
}

async function saveSamsungConfig(){
  if(!samsungCurrentId)return;
  const begin=document.getElementById('samsung-inactive-begin').value;
  const end=document.getElementById('samsung-inactive-end').value;
  const r=await fetch('/api/samsung/'+encodeURIComponent(samsungCurrentId)+'/config',{
    method:'PUT',headers:{'Content-Type':'application/json'},
    body:JSON.stringify({
      poll_interval_minutes:parseInt(document.getElementById('samsung-poll').value,10)||60,
      inactive_begin:begin?begin.slice(0,5):'',
      inactive_end:end?end.slice(0,5):'',
      crop_format:document.getElementById('samsung-crop-format').value,
      display_width:parseInt(document.getElementById('samsung-display-width').value,10)||0,
      display_height:parseInt(document.getElementById('samsung-display-height').value,10)||0
    })
  });
  if(!r.ok){alert('Save failed: '+(await r.text()));return;}
  loadSamsungFrame();
}

document.getElementById('samsung-file-input').addEventListener('change',async e=>{
  if(!samsungCurrentId||!e.target.files.length)return;
  const fd=new FormData(); fd.append('file',e.target.files[0]);
  const r=await fetch('/api/samsung/'+encodeURIComponent(samsungCurrentId)+'/image',{method:'POST',body:fd});
  if(!r.ok){alert('Upload failed: '+(await r.text()));return;}
  loadSamsungFrame();
});
const sz=document.getElementById('samsung-upload-zone');
sz.addEventListener('dragover',e=>{e.preventDefault();sz.style.borderColor='#1a1a2e'});
sz.addEventListener('dragleave',()=>{sz.style.borderColor='#ccc'});
sz.addEventListener('drop',async e=>{
  e.preventDefault();sz.style.borderColor='#ccc';
  if(!samsungCurrentId||!e.dataTransfer.files.length)return;
  const fd=new FormData(); fd.append('file',e.dataTransfer.files[0]);
  await fetch('/api/samsung/'+encodeURIComponent(samsungCurrentId)+'/image',{method:'POST',body:fd});
  loadSamsungFrame();
});
</script>

<!-- ── Crop editor modal ─────────────────────────────────────── -->
<div id="crop-modal">
<div id="crop-ui">
  <div id="crop-toolbar">
    <label>Frame format</label>
    <select id="crop-format" onchange="onFormatChange()">
      <option value="4:3">Landscape 4:3</option>
      <option value="3:4">Portrait 3:4</option>
      <option value="16:9">Landscape 16:9</option>
      <option value="9:16">Portrait 9:16</option>
      <option value="1:1">Square 1:1</option>
    </select>
    <button class="btn btn-primary btn-sm" onclick="saveCrop()">Save</button>
    <button id="crop-delete-btn" class="btn btn-sm" style="background:#c0392b;color:#fff;display:none" onclick="deleteCrop()">Delete</button>
    <button class="btn btn-sm" onclick="closeCrop()">Close</button>
    <span id="crop-hint"></span>
  </div>
  <div id="crop-stage">
    <img id="crop-img" draggable="false">
    <div id="crop-box">
      <div class="ch" data-h="tl"></div>
      <div class="ch" data-h="tr"></div>
      <div class="ch" data-h="bl"></div>
      <div class="ch" data-h="br"></div>
    </div>
  </div>
</div>
</div>

<script>
// ── Crop editor ──────────────────────────────────────────────────────────────
const FORMATS = {
  '4:3':  {ar:4/3},
  '3:4':  {ar:3/4},
  '16:9': {ar:16/9},
  '9:16': {ar:9/16},
  '1:1':  {ar:1},
};

let cropId=null, cropAR=4/3, cropFmt='4:3';
let cropRect={x:0,y:0,w:1,h:1};    // normalised 0-1 (relative to source image)
let cropImgAR=1;                    // source image aspect ratio (w/h), set onload
let imgDisp={x:0,y:0,w:0,h:0};     // image rect in stage pixel coords
let allCrops={};
let drag=null;  // {type:'move'|handle, sx,sy, cr0, corner?}

const cropModal  = ()=>document.getElementById('crop-modal');
const cropImgEl  = ()=>document.getElementById('crop-img');
const cropBoxEl  = ()=>document.getElementById('crop-box');
const cropStageEl= ()=>document.getElementById('crop-stage');

// defaultCrop returns a centered crop rect in normalised source-image coordinates
// that achieves targetAR visually, given the source image has aspect ratio imgAR.
function defaultCrop(targetAR, imgAR){
  if(targetAR > imgAR){
    // target is wider than source: use full width, letterbox height
    const h = imgAR / targetAR;
    return {x:0, y:(1-h)/2, w:1, h};
  } else {
    // target is taller (or equal): use full height, pillarbox width
    const w = targetAR / imgAR;
    return {x:(1-w)/2, y:0, w, h:1};
  }
}

function updateDeleteBtn(){
  document.getElementById('crop-delete-btn').style.display = allCrops[cropFmt] ? '' : 'none';
}

function openCrop(id){
  cropId=id; allCrops={...(imageCropsCache[id]||{})};
  cropFmt = Object.keys(FORMATS).find(k=>allCrops[k]) || '4:3';
  cropAR  = FORMATS[cropFmt].ar;
  document.getElementById('crop-format').value = cropFmt;
  imgDisp  = {x:0,y:0,w:0,h:0};

  const img = cropImgEl();
  img.onload = ()=>{
    cropImgAR = img.naturalWidth / img.naturalHeight;
    layoutImg();
    cropRect = allCrops[cropFmt] ? {...allCrops[cropFmt]} : defaultCrop(cropAR, cropImgAR);
    renderBox();
    renderOverlays();
    updateDeleteBtn();
  };
  img.src = '/images/'+id+'/preview';
  cropModal().classList.add('open');
}

function closeCrop(){ cropModal().classList.remove('open'); cropId=null; }

function onFormatChange(){
  cropFmt = document.getElementById('crop-format').value;
  cropAR  = FORMATS[cropFmt].ar;
  cropRect = allCrops[cropFmt] ? {...allCrops[cropFmt]} : defaultCrop(cropAR, cropImgAR);
  renderBox();
  renderOverlays();
  updateDeleteBtn();
}

async function saveCrop(){
  if(!cropId) return;
  const r = await fetch('/api/images/'+cropId+'/crop',{
    method:'POST', headers:{'Content-Type':'application/json'},
    body:JSON.stringify({format:cropFmt, ...cropRect})
  });
  if(!r.ok){ alert('Save failed: '+(await r.text())); return; }
  allCrops[cropFmt]={...cropRect};
  if(!imageCropsCache[cropId]) imageCropsCache[cropId]={};
  imageCropsCache[cropId][cropFmt]={...cropRect};
  renderOverlays();
  updateDeleteBtn();
  refreshThumb(cropId);
}

async function deleteCrop(){
  if(!cropId || !allCrops[cropFmt]) return;
  const r = await fetch('/api/images/'+cropId+'/crop?format='+encodeURIComponent(cropFmt),{method:'DELETE'});
  if(!r.ok){ alert('Delete failed: '+(await r.text())); return; }
  delete allCrops[cropFmt];
  if(imageCropsCache[cropId]) delete imageCropsCache[cropId][cropFmt];
  cropRect = defaultCrop(cropAR, cropImgAR);
  renderBox();
  renderOverlays();
  updateDeleteBtn();
  refreshThumb(cropId);
}

function refreshThumb(id){
  const card = document.getElementById('card-'+id);
  if(card){ const t=card.querySelector('img'); if(t) t.src=t.src.replace(/\?t=\d+/,'')+'?t='+Date.now(); }
}

// ── layout ───────────────────────────────────────────────────────────────────
function layoutImg(){
  const stage = cropStageEl(), img = cropImgEl();
  const sw = stage.clientWidth, sh = stage.clientHeight;
  const iw = img.naturalWidth,  ih = img.naturalHeight;
  const scale = Math.min(sw/iw, sh/ih);
  const dw = iw*scale, dh = ih*scale;
  const ox = (sw-dw)/2, oy = (sh-dh)/2;
  img.style.cssText = 'left:'+ox+'px;top:'+oy+'px;width:'+dw+'px;height:'+dh+'px';
  imgDisp = {x:ox,y:oy,w:dw,h:dh};
}

function renderBox(){
  if(!imgDisp.w) return;
  const {x,y,w,h} = cropRect;
  const bx = imgDisp.x + x*imgDisp.w;
  const by = imgDisp.y + y*imgDisp.h;
  const bw = w*imgDisp.w, bh = h*imgDisp.h;
  cropBoxEl().style.cssText = 'left:'+bx+'px;top:'+by+'px;width:'+bw+'px;height:'+bh+'px';
  document.getElementById('crop-hint').textContent =
    Math.round(x*100)+'%, '+Math.round(y*100)+'%  —  '+Math.round(w*100)+'% × '+Math.round(h*100)+'%';
}

// renderOverlays redraws dim dashed boxes for all saved crops except the active one.
function renderOverlays(){
  if(!imgDisp.w) return;
  const stage = cropStageEl();
  stage.querySelectorAll('.crop-dim,.crop-dim-label').forEach(el=>el.remove());
  for(const [fmt, rect] of Object.entries(allCrops)){
    if(fmt===cropFmt) continue;
    const bx = imgDisp.x + rect.x*imgDisp.w;
    const by = imgDisp.y + rect.y*imgDisp.h;
    const bw = rect.w*imgDisp.w, bh = rect.h*imgDisp.h;
    const div = document.createElement('div');
    div.className = 'crop-dim';
    div.style.cssText = 'left:'+bx+'px;top:'+by+'px;width:'+bw+'px;height:'+bh+'px';
    const lbl = document.createElement('span');
    lbl.className = 'crop-dim-label';
    lbl.textContent = fmt;
    div.appendChild(lbl);
    stage.appendChild(div);
  }
}

// ── drag ─────────────────────────────────────────────────────────────────────
function clamp(v,lo,hi){ return Math.max(lo,Math.min(hi,v)); }

function applyDrag(e){
  if(!drag||!imgDisp.w) return;
  const dx=(e.clientX-drag.sx)/imgDisp.w;
  const dy=(e.clientY-drag.sy)/imgDisp.h;
  let {x,y,w,h}=drag.cr0;

  if(drag.type==='move'){
    x=clamp(x+dx, 0, 1-w);
    y=clamp(y+dy, 0, 1-h);
    cropRect={x,y,w,h};
  } else {
    // corner resize — anchor is opposite corner
    const c=drag.corner;
    const ax = c.includes('r') ? x     : x+w;  // anchor x
    const ay = c.includes('b') ? y     : y+h;  // anchor y
    let fx  = c.includes('r') ? x+w+dx : x+dx;
    let fy  = c.includes('b') ? y+h+dy : y+dy;
    fx=clamp(fx,0,1); fy=clamp(fy,0,1);

    let nw=Math.abs(fx-ax), nh=Math.abs(fy-ay);
    // normAR: the w/h ratio in normalised coords that produces visual cropAR.
    // (nw * imgDisp.w) / (nh * imgDisp.h) = cropAR  =>  nw/nh = cropAR / cropImgAR
    const normAR = cropAR / cropImgAR;
    if(nw/normAR > nh){ nh=nw/normAR; } else { nw=nh*normAR; }
    // clamp to image bounds from anchor, then re-enforce ratio
    nw=Math.min(nw, c.includes('r') ? 1-ax : ax);
    nh=Math.min(nh, c.includes('b') ? 1-ay : ay);
    if(nw/normAR > nh){ nw=nh*normAR; } else { nh=nw/normAR; }

    const nx = c.includes('r') ? ax     : ax-nw;
    const ny = c.includes('b') ? ay     : ay-nh;
    cropRect={x:nx,y:ny,w:nw,h:nh};
  }
  renderBox();
}

cropBoxEl().addEventListener('mousedown',e=>{
  if(e.target.classList.contains('ch')) return;
  drag={type:'move',sx:e.clientX,sy:e.clientY,cr0:{...cropRect}};
  e.preventDefault();
});
document.querySelectorAll('.ch').forEach(h=>{
  h.addEventListener('mousedown',e=>{
    drag={type:'corner',corner:h.dataset.h,sx:e.clientX,sy:e.clientY,cr0:{...cropRect}};
    e.preventDefault(); e.stopPropagation();
  });
});
document.addEventListener('mousemove',applyDrag);
document.addEventListener('mouseup',()=>{ drag=null; });
window.addEventListener('resize',()=>{ if(imgDisp.w){ layoutImg(); renderBox(); renderOverlays(); } });
</script>
</body>
</html>`
