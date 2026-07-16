//go:build inkjoybridge

package main

// inkjoyBridgePageHTML is the bridge-owned InkJoy configuration page.
// API calls use relative paths under /inkjoy/api/… (hub proxies /{bridge_id}/… over MQTT).
const inkjoyBridgePageHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<base href="/inkjoy/">
<title>InkJoy</title>
<style>
*{box-sizing:border-box}
body{font-family:system-ui,-apple-system,sans-serif;margin:0;padding:1rem 1.25rem;background:#f8f9fa;color:#222}
h3{margin:0}
.card{background:#fff;border:1px solid #ddd;border-radius:8px;padding:1rem;margin-bottom:1rem}
.section-label{font-size:.75rem;font-weight:600;text-transform:uppercase;color:#888;margin:.75rem 0 .35rem}
.frame-list-item{display:flex;align-items:center;gap:.5rem;padding:.45rem .6rem;border-radius:6px;cursor:pointer;margin-bottom:.25rem}
.frame-list-item:hover{background:#f0f0f0}
.frame-list-item.selected{background:#e7f1ff}
.dot{width:8px;height:8px;border-radius:50%;flex-shrink:0}
.dot.online{background:#28a745}
.dot.offline{background:#ccc}
.badge{display:inline-block;padding:.15rem .45rem;border-radius:4px;font-size:.75rem;font-weight:600}
.badge.online{background:#d4edda;color:#155724}
.badge.offline{background:#eee;color:#666}
.btn{border:1px solid #ccc;background:#fff;border-radius:4px;padding:.35rem .65rem;cursor:pointer;font-size:.85rem}
.btn-primary{background:#007bff;color:#fff;border-color:#007bff}
.btn-sm{padding:.25rem .5rem;font-size:.8rem}
.info-grid{display:grid;grid-template-columns:auto 1fr;gap:.35rem .75rem;font-size:.9rem;margin-top:.75rem}
.info-grid .label{color:#888}
.last-image-preview{max-width:100%;max-height:280px;border:1px solid #ddd;border-radius:4px;display:block}
.layout{display:flex;gap:1.5rem;align-items:flex-start;flex-wrap:wrap}
.sidebar{min-width:220px;flex:0 0 220px}
.editor{flex:1;min-width:280px;display:none}
.editor.open{display:block}
.modal-backdrop{display:none;position:fixed;inset:0;background:#000a;z-index:1000;align-items:center;justify-content:center}
.modal{background:#fff;border-radius:10px;padding:1.25rem;min-width:300px;max-width:420px;width:90%}
input[type=text],input[type=password],input[type=time]{padding:.35rem .5rem;border:1px solid #ccc;border-radius:4px}
.mqtt-grid{display:grid;grid-template-columns:1fr 1fr;gap:1rem;margin-top:.75rem}
@media(max-width:900px){.mqtt-grid{grid-template-columns:1fr}}
.mqtt-col h4{font-size:.95rem;margin:0 0 .65rem;color:#333}
.mqtt-log{max-height:50vh;overflow-y:auto;overscroll-behavior:contain;-webkit-overflow-scrolling:touch;display:flex;flex-direction:column;gap:.5rem}
.mqtt-entry{border:1px solid #e0e0e0;border-radius:6px;padding:.6rem .75rem;font-size:.8rem;background:#fafafa}
.mqtt-entry.clampable{cursor:pointer}
.mqtt-entry.clampable:not(.expanded):hover{border-color:#bbb;background:#f5f5f5}
.mqtt-entry .meta{display:flex;flex-wrap:wrap;gap:.35rem .6rem;align-items:center;margin-bottom:.35rem;color:#555}
.mqtt-entry .time{font-family:monospace;color:#888}
.mqtt-entry .dir{font-weight:600;color:#1a1a2e}
.mqtt-entry .action{background:#e8eaf6;color:#1a1a2e;padding:1px 6px;border-radius:4px;font-family:monospace;font-size:.75rem}
.mqtt-entry .note{color:#856404;background:#fff3cd;padding:1px 6px;border-radius:4px;font-size:.75rem}
.mqtt-entry .topic{font-family:monospace;font-size:.72rem;color:#666;word-break:break-all;margin-bottom:.25rem}
.mqtt-entry pre{margin:0;white-space:pre-wrap;word-break:break-word;font-size:.72rem;line-height:1.35;background:#fff;border:1px solid #eee;border-radius:4px;padding:.4rem .5rem;user-select:text;-webkit-user-select:text;cursor:text}
.mqtt-entry.clampable:not(.expanded) pre{display:-webkit-box;-webkit-line-clamp:6;-webkit-box-orient:vertical;overflow:hidden}
.mqtt-expand-hint{font-size:.68rem;color:#888;margin-top:.2rem}
.mqtt-copy{margin-left:auto;font-size:.68rem;padding:2px 8px;border:1px solid #ccc;border-radius:4px;background:#fff;cursor:pointer;color:#333}
.mqtt-copy:hover{background:#f0f0f0}
.mqtt-expand-hint{font-size:.68rem;color:#888;margin-top:.2rem}
.mqtt-empty{color:#888;font-size:.9rem;margin:0}
</style>
</head>
<body>
<div id="adopt-modal" class="modal-backdrop">
  <div class="modal">
    <h3>Adopt frame</h3>
    <p id="adopt-frame-name" style="font-family:monospace;color:#555"></p>
    <label style="display:block;margin:.75rem 0">WiFi network (SSID)<br><input id="adopt-ssid" style="width:100%;margin-top:.3rem"></label>
    <label style="display:block;margin:.75rem 0">WiFi password<br><input id="adopt-pwd" type="password" style="width:100%;margin-top:.3rem"></label>
    <div id="adopt-status" style="font-size:.9rem;color:#666;min-height:1.2em;margin:.5rem 0"></div>
    <div style="display:flex;gap:.5rem;justify-content:flex-end">
      <button class="btn btn-sm" onclick="closeAdopt()">Cancel</button>
      <button class="btn btn-sm btn-primary" id="adopt-submit-btn" onclick="submitAdopt()">Adopt</button>
    </div>
  </div>
</div>
<div class="layout">
  <div class="sidebar">
    <div class="section-label">Frames</div>
    <div id="frame-list"><p style="color:#888;font-size:.9rem">Loading…</p></div>
    <div style="margin-top:.75rem">
      <button class="btn btn-sm btn-primary" id="ble-scan-btn" onclick="startBLEScan()">Find new frames</button>
      <span id="ble-scan-status" style="display:block;font-size:.8rem;color:#666;margin-top:.4rem"></span>
    </div>
    <div id="ble-scan-results" style="margin-top:.75rem"></div>
  </div>
  <div id="editor" class="editor">
    <div class="card">
      <div style="display:flex;align-items:center;gap:.75rem;margin-bottom:1rem">
        <div class="dot" id="dot"></div>
        <h3 id="title"></h3>
        <span id="status-badge" class="badge"></span>
      </div>
      <div style="display:flex;gap:.5rem;margin-bottom:.75rem">
        <input id="name-input" placeholder="Friendly name" style="flex:1">
        <button class="btn btn-sm btn-primary" onclick="saveName()">Rename</button>
      </div>
      <label style="display:flex;align-items:center;gap:.4rem;font-size:.9rem;cursor:pointer;margin-bottom:1rem">
        <input type="checkbox" id="portrait" onchange="savePortrait()"> Portrait orientation (3:4 crop, rotate 90°)
      </label>
      <div class="info-grid" id="info"></div>
    </div>
    <div class="card">
      <div class="section-label" style="margin-top:0">Currently displayed</div>
      <div id="last-image"><p style="color:#888;font-size:.9rem">Nothing sent yet this session.</p></div>
      <div style="margin-top:.75rem">
        <button class="btn btn-sm" style="background:#6c757d;color:#fff" onclick="doRefresh()">Refresh display</button>
      </div>
      <p style="font-size:.8rem;color:#888;margin:.5rem 0 0">To send from the shared album, use <strong>Album → Send</strong> on the hub.</p>
    </div>
    <div class="card">
      <div class="section-label" style="margin-top:0">Sleep schedule</div>
      <div style="display:flex;gap:1rem;align-items:center;flex-wrap:wrap">
        <label style="font-size:.9rem">Sleep from <input type="time" id="sleep-begin"></label>
        <label style="font-size:.9rem">to <input type="time" id="sleep-end"></label>
        <button class="btn btn-sm btn-primary" onclick="saveSleep()">Save</button>
        <button class="btn btn-sm" style="background:#6c757d;color:#fff" onclick="clearSleep()">Clear (always on)</button>
      </div>
    </div>
    <div class="card" style="border-color:#f5c6cb">
      <div class="section-label" style="margin-top:0;color:#dc3545">Danger zone</div>
      <button class="btn btn-sm" style="background:#dc3545;color:#fff" onclick="deleteDevice()">Remove frame from hub</button>
    </div>
  </div>
</div>
<div class="card">
  <div class="section-label" style="margin-top:0">MQTT traffic</div>
  <p style="color:#666;font-size:.85rem;margin:.35rem 0 0">Frame broker and cloud upstream — last 20 messages per direction. Newest at top.</p>
  <label style="display:flex;align-items:center;gap:.5rem;margin:.25rem 0 .75rem;font-size:.85rem;color:#555">
    <input type="checkbox" id="mqtt-hide-noisy" onchange="onMqttHideNoisyChange()"> Hide login/heart noise (keeps play &amp; play_ack)
  </label>
  <div class="mqtt-grid">
    <div class="mqtt-col">
      <h4>Frame MQTT</h4>
      <div id="mqtt-local" class="mqtt-log"><p class="mqtt-empty">No messages yet.</p></div>
    </div>
    <div class="mqtt-col">
      <h4>Cloud upstream</h4>
      <div id="mqtt-upstream" class="mqtt-log"><p class="mqtt-empty">No messages yet.</p></div>
    </div>
  </div>
</div>
<script>
let devices=[], currentId=null, previewKey=null, adoptTarget=null;
async function loadDevices(){
  try{
    const r=await fetch('/inkjoy/api/devices');
    if(!r.ok) throw new Error('HTTP '+r.status);
    devices=await r.json();
    renderList();
    if(currentId){
      const d=devices.find(x=>x.id===currentId);
      if(d) updateStatus(d); else { currentId=null; hideEditor(); }
    }
  }catch(e){
    document.getElementById('frame-list').innerHTML='<p style="color:#c00;font-size:.9rem">Failed to load frames: '+e.message+'</p>';
  }
}
function hideEditor(){
  const el=document.getElementById('editor');
  el.classList.remove('open');
  el.style.display='none';
}
function showEditor(){
  const el=document.getElementById('editor');
  el.classList.add('open');
  el.style.display='block';
}
function escAttr(s){
  return String(s).replace(/&/g,'&amp;').replace(/"/g,'&quot;').replace(/'/g,'&#39;');
}
function renderList(){
  const el=document.getElementById('frame-list');
  if(!devices.length){ el.innerHTML='<p style="color:#888;font-size:.9rem">No InkJoy frames yet.</p>'; return; }
  el.innerHTML=devices.map(d=>{
    const label=d.name||d.mac||d.id;
    const sel=d.id===currentId?' selected':'';
    return '<div class="frame-list-item'+sel+'" data-id="'+escAttr(d.id)+'" onclick="openFrame(this.dataset.id)">'+
      '<div class="dot '+(d.connected?'online':'offline')+'"></div>'+
      '<span style="font-weight:500">'+label+'</span>'+
      (d.battery?'<span style="margin-left:auto;font-size:.8rem;color:#666">🔋'+d.battery+'%</span>':'')+
      '</div>';
  }).join('');
}
function openFrame(id){
  currentId=id; previewKey=null;
  showEditor();
  const d=devices.find(x=>x.id===id);
  if(!d) return;
  document.getElementById('name-input').value=d.name||'';
  document.getElementById('portrait').checked=!!d.portrait;
  document.getElementById('sleep-begin').value=d.sleep_begin_time||'';
  document.getElementById('sleep-end').value=d.sleep_end_time||'';
  updateStatus(d);
  renderList();
}
function offlineLabel(d){
  if(d.last_action==='shutdown'&&d.sleep_end_time) return 'deep sleep · back '+d.sleep_end_time;
  return 'offline';
}
function updateStatus(d){
  document.getElementById('title').textContent=d.name||d.mac||d.id;
  document.getElementById('dot').className='dot '+(d.connected?'online':'offline');
  const badge=document.getElementById('status-badge');
  badge.className='badge '+(d.connected?'online':'offline');
  badge.textContent=d.connected?'online':offlineLabel(d);
  const ago=d.last_seen?timeAgo(d.last_seen):'never';
  document.getElementById('info').innerHTML=
    '<span class="label">MAC</span><span style="font-family:monospace">'+d.mac+'</span>'+
    '<span class="label">Firmware</span><span>'+(d.firmware||'—')+'</span>'+
    '<span class="label">Battery</span><span>'+(d.battery?d.battery+'%':'—')+'</span>'+
    '<span class="label">Last seen</span><span>'+ago+'</span>';
  const li=document.getElementById('last-image');
  if(d.last_image_id){
    const q=new URLSearchParams();
    if(d.portrait) q.set('portrait','1');
    if(d.last_overlay_hash) q.set('overlay', d.last_overlay_hash);
    const qs=q.toString();
    const url='/images/'+encodeURIComponent(d.last_image_id)+'/frame-preview'+(qs?'?'+qs:'');
    const key='album:'+d.last_image_id;
    if(key!==previewKey){
      previewKey=key;
      li.innerHTML='<img class="last-image-preview" src="'+url+'" alt="currently displayed">';
    }
  }else if(previewKey!==null){
    previewKey=null;
    li.innerHTML='<p style="color:#888;font-size:.9rem">Nothing sent yet.</p>';
  }
}
function timeAgo(iso){
  const s=Math.round((Date.now()-new Date(iso))/1000);
  if(s<60) return s+'s ago';
  if(s<3600) return Math.round(s/60)+'m ago';
  return Math.round(s/3600)+'h ago';
}
async function saveName(){
  if(!currentId) return;
  await fetch('/inkjoy/api/devices/'+encodeURIComponent(currentId),{method:'PATCH',headers:{'Content-Type':'application/json'},body:JSON.stringify({name:document.getElementById('name-input').value.trim()})});
  await loadDevices();
}
async function savePortrait(){
  if(!currentId) return;
  await fetch('/inkjoy/api/devices/'+encodeURIComponent(currentId),{method:'PATCH',headers:{'Content-Type':'application/json'},body:JSON.stringify({portrait:document.getElementById('portrait').checked})});
}
async function doRefresh(){
  if(!currentId) return;
  await fetch('/inkjoy/api/devices/'+encodeURIComponent(currentId)+'/refresh',{method:'POST'});
}
async function saveSleep(){
  if(!currentId) return;
  await fetch('/inkjoy/api/devices/'+encodeURIComponent(currentId)+'/sleep',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({beginTime:document.getElementById('sleep-begin').value||'00:00',endTime:document.getElementById('sleep-end').value||'00:00',mode:2})});
}
async function clearSleep(){
  document.getElementById('sleep-begin').value='00:00';
  document.getElementById('sleep-end').value='00:00';
  await saveSleep();
}
async function deleteDevice(){
  if(!currentId||!confirm('Remove this frame from the hub?')) return;
  await fetch('/inkjoy/api/devices/'+encodeURIComponent(currentId),{method:'DELETE'});
  currentId=null;
  hideEditor();
  await loadDevices();
}
let bleScanResults=[];
async function startBLEScan(){
  const btn=document.getElementById('ble-scan-btn');
  const st=document.getElementById('ble-scan-status');
  const res=document.getElementById('ble-scan-results');
  btn.disabled=true; st.textContent='Scanning…'; res.innerHTML='';
  try{
    const r=await fetch('/inkjoy/api/ble/scan',{method:'POST'});
    bleScanResults=await r.json();
    if(!bleScanResults.length){ st.textContent='No frames found nearby.'; return; }
    st.textContent=bleScanResults.length+' frame(s) found:';
    res.innerHTML=bleScanResults.map((f,i)=>'<div class="frame-list-item" style="background:#f0f7ff" onclick="openAdoptIdx('+i+')"><div class="dot offline"></div><span>'+f.name+'</span><button class="btn btn-sm btn-primary" style="margin-left:auto;font-size:.75rem" onclick="event.stopPropagation();openAdoptIdx('+i+')">Adopt</button></div>').join('');
  }catch(e){ st.textContent='Scan failed: '+e.message; }
  finally{ btn.disabled=false; }
}
function openAdoptIdx(i){ openAdopt(bleScanResults[i]); }
function openAdopt(frame){
  adoptTarget=frame;
  document.getElementById('adopt-frame-name').textContent=frame.name+'  '+frame.mac;
  document.getElementById('adopt-modal').style.display='flex';
}
function closeAdopt(){ document.getElementById('adopt-modal').style.display='none'; adoptTarget=null; }
async function submitAdopt(){
  if(!adoptTarget) return;
  const ssid=document.getElementById('adopt-ssid').value.trim();
  const pwd=document.getElementById('adopt-pwd').value;
  const st=document.getElementById('adopt-status');
  st.textContent='Connecting via Bluetooth…';
  try{
    const r=await fetch('/inkjoy/api/ble/adopt',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({address:adoptTarget.address,ssid,wifi_pwd:pwd})});
    if(!r.ok) throw new Error(await r.text());
    st.textContent='Adopted! Waiting for frame to connect…';
    bleScanResults=[];
    document.getElementById('ble-scan-results').innerHTML='';
    document.getElementById('ble-scan-status').textContent='';
    setTimeout(()=>{ closeAdopt(); loadDevices(); startBLEScan(); }, 3000);
  }catch(e){ st.textContent='Failed: '+e.message; }
}
loadDevices();
setInterval(loadDevices, 5000);
function mqttEsc(s){ return String(s).replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;'); }
let mqttExpanded=new Set();
let mqttHideNoisyInitialized=false;
function initMqttHideNoisy(){
  if(mqttHideNoisyInitialized) return;
  mqttHideNoisyInitialized=true;
  const cb=document.getElementById('mqtt-hide-noisy');
  if(!cb) return;
  cb.checked=localStorage.getItem('mqtt-hide-noisy')==='1';
}
function onMqttHideNoisyChange(){
  const cb=document.getElementById('mqtt-hide-noisy');
  if(cb) localStorage.setItem('mqtt-hide-noisy',cb.checked?'1':'0');
  for(const id of ['mqtt-local','mqtt-upstream']){
    const el=document.getElementById(id);
    if(el) delete el.dataset.mqttSig;
  }
  loadMQTTLogs();
}
function mqttFrameEntryImportant(e){
  const a=e.action||'';
  if(a==='play'||a==='play_ack') return true;
  const body=e.body||'';
  return body.indexOf('"action":"play"')>=0||body.indexOf('"action":"play_ack"')>=0;
}
function mqttFrameEntryHidden(e){
  if(mqttFrameEntryImportant(e)) return false;
  if(!e.action) return false;
  return e.action==='login'||e.action==='heart'||e.action==='login_ack'||e.action==='heart_ack';
}
function filterMQTTFrameEntries(entries){
  const cb=document.getElementById('mqtt-hide-noisy');
  if(!cb||!cb.checked) return entries;
  return (entries||[]).filter(e=>!mqttFrameEntryHidden(e));
}
function mqttEntryKey(e){ return e.time+'\0'+e.topic+'\0'+e.dir; }
function mqttBodyLong(body){ return body && (body.length>180 || body.split('\n').length>6); }
function mqttSelectionIn(el){
  const sel=window.getSelection();
  if(!sel||sel.isCollapsed||!sel.rangeCount) return false;
  let node=sel.anchorNode;
  if(!node) return false;
  if(node.nodeType===Node.TEXT_NODE) node=node.parentElement;
  return node&&el.contains(node);
}
function mqttEntryHTML(e){
  const note=e.note?'<span class="note">'+mqttEsc(e.note)+'</span>':'';
  const action=e.action?'<span class="action">'+mqttEsc(e.action)+'</span>':'';
  const longBody=mqttBodyLong(e.body);
  const key=mqttEntryKey(e);
  const expanded=mqttExpanded.has(key);
  const cls='mqtt-entry'+(longBody?' clampable':'')+(expanded?' expanded':'');
  const body=e.body?'<pre>'+mqttEsc(e.body)+'</pre>':'';
  const copyBtn=e.body?'<button type="button" class="mqtt-copy" onclick="event.stopPropagation();copyMQTTPayload(this)" title="Copy payload">Copy</button>':'';
  const hint=longBody&&!expanded?'<div class="mqtt-expand-hint">Click row to expand</div>':'';
  const expandAttr=longBody?' onclick="toggleMQTTEntry(this,event)"':'';
  const keyAttr=' data-mqtt-key="'+encodeURIComponent(key)+'"';
  return '<div class="'+cls+'"'+keyAttr+expandAttr+'><div class="meta"><span class="time">'+mqttEsc(e.time)+'</span><span class="dir">'+mqttEsc(e.dir)+'</span>'+action+note+copyBtn+'</div><div class="topic">'+mqttEsc(e.topic)+'</div>'+body+hint+'</div>';
}
function toggleMQTTEntry(el,ev){
  if(ev&&(ev.target.closest('pre')||ev.target.closest('.mqtt-copy'))) return;
  const key=decodeURIComponent(el.dataset.mqttKey);
  if(mqttExpanded.has(key)) mqttExpanded.delete(key); else mqttExpanded.add(key);
  el.classList.toggle('expanded');
  const hint=el.querySelector('.mqtt-expand-hint');
  if(hint) hint.textContent=el.classList.contains('expanded')?'Click to collapse':'Click row to expand';
}
async function copyMQTTPayload(btn){
  const pre=btn.closest('.mqtt-entry')?.querySelector('pre');
  if(!pre) return;
  const text=pre.textContent;
  try{
    await navigator.clipboard.writeText(text);
  }catch(_){
    const r=document.createRange();
    r.selectNodeContents(pre);
    const sel=window.getSelection();
    sel.removeAllRanges();
    sel.addRange(r);
    document.execCommand('copy');
    sel.removeAllRanges();
  }
  const prev=btn.textContent;
  btn.textContent='Copied';
  setTimeout(()=>{ btn.textContent=prev; },1500);
}
function applyMQTTColumn(el,list,sig){
  if(!list.length){
    el.innerHTML='<p class="mqtt-empty">No messages yet.</p>';
    el.dataset.mqttSig='';
    return;
  }
  const keys=list.map(mqttEntryKey);
  const children=[...el.querySelectorAll('.mqtt-entry[data-mqtt-key]')];
  const childKeys=children.map(c=>decodeURIComponent(c.dataset.mqttKey));
  if(children.length){
    const offset=keys.length-childKeys.length;
    if(offset>=0&&offset<=5){
      let suffixMatch=true;
      for(let i=0;i<childKeys.length;i++){
        if(keys[i+offset]!==childKeys[i]){ suffixMatch=false; break; }
      }
      if(suffixMatch){
        if(offset===0){
          el.dataset.mqttSig=sig;
          return;
        }
        const y=el.scrollTop;
        const empty=el.querySelector('.mqtt-empty');
        if(empty) empty.remove();
        const frag=document.createDocumentFragment();
        for(let i=0;i<offset;i++){
          const tmp=document.createElement('div');
          tmp.innerHTML=mqttEntryHTML(list[i]);
          frag.appendChild(tmp.firstElementChild);
        }
        el.insertBefore(frag,el.firstChild);
        while(el.querySelectorAll('.mqtt-entry').length>list.length){
          el.querySelector('.mqtt-entry:last-child')?.remove();
        }
        el.dataset.mqttSig=sig;
        el.scrollTop=y;
        return;
      }
    }
  }
  const y=el.scrollTop;
  el.innerHTML=list.map(mqttEntryHTML).join('');
  el.scrollTop=y;
  el.dataset.mqttSig=sig;
}
function updateMQTTColumn(id,entries){
  const el=document.getElementById(id);
  if(!el) return;
  const list=(entries||[]).slice().reverse();
  const sig=list.map(mqttEntryKey).join('\0');
  if(el.dataset.mqttSig===sig) return;
  if(mqttSelectionIn(el)){
    el.dataset.mqttPending='1';
    return;
  }
  delete el.dataset.mqttPending;
  applyMQTTColumn(el,list,sig);
}
let mqttSelectionFlushTimer=null;
document.addEventListener('selectionchange',()=>{
  let needsFlush=false;
  for(const id of ['mqtt-local','mqtt-upstream']){
    const el=document.getElementById(id);
    if(!el||!el.dataset.mqttPending) continue;
    if(mqttSelectionIn(el)) return;
    delete el.dataset.mqttPending;
    needsFlush=true;
  }
  if(needsFlush){
    clearTimeout(mqttSelectionFlushTimer);
    mqttSelectionFlushTimer=setTimeout(loadMQTTLogs,150);
  }
});
async function loadMQTTLogs(){
  try{
    initMqttHideNoisy();
    const r=await fetch('/inkjoy/api/mqtt/logs');
    if(!r.ok) return;
    const data=await r.json();
    updateMQTTColumn('mqtt-local',filterMQTTFrameEntries(data.local));
    updateMQTTColumn('mqtt-upstream',filterMQTTFrameEntries(data.upstream));
  }catch(_){}
}
loadMQTTLogs();
setInterval(loadMQTTLogs, 1000);
</script>
</body>
</html>`
