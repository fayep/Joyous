let joyousUIRevision=JOYOUS_UI_REVISION;

function joyousUIBusy(){
  if(albumCaptionEditingId) return true;
  if(albumZoomId) return true;
  if(albumReorderBusy) return true;
  if(albumFilterEditing) return true;
  const m=document.getElementById('crop-modal');
  return !!(m&&m.classList.contains('open'));
}

// joyousAlbumGridBusy is joyousUIBusy() minus the zoom-open check — used only by
// applyImagesEvent. Unlike a full-page reload (which would yank the zoom modal out from under
// someone), rebuilding the grid behind an open zoom is harmless: the modal holds no reference to
// grid DOM nodes, and the rotate buttons already patch the zoomed image directly from their own
// PATCH response. Gating this the same as joyousUIBusy() meant the grid silently missed every
// "images" update while zoomed — e.g. right after using the rotate buttons — until the next hard
// reload happened to rebuild it from scratch.
function joyousAlbumGridBusy(){
  if(albumCaptionEditingId) return true;
  if(albumReorderBusy) return true;
  if(albumFilterEditing) return true;
  const m=document.getElementById('crop-modal');
  return !!(m&&m.classList.contains('open'));
}

// applyRevisionEvent handles the "revision" event the hub sends as part of every event-stream
// handshake (see event_bus.go's handleEvents) — on first connect AND on every automatic
// EventSource reconnect. A hub restart (new build deployed) drops the connection, so the very
// reconnect that follows carries the new revision — no separate polling interval needed to
// catch it, unlike the old 60s /api/ui/revision poll this replaces.
function applyRevisionEvent(data){
  if(joyousUIBusy()) return;
  if(data&&data.revision&&joyousUIRevision&&data.revision!==joyousUIRevision){
    location.reload();
    return;
  }
  if(data&&data.revision) joyousUIRevision=data.revision;
}

let devices=[], images=[], activeTab='album', inkjoyBridgeOnline=false, nixplayBridgeOnline=false;
let schedState={}; // deviceId -> {albumId, times:[...], enabled} — in-progress schedule editor state

// joyousSessionId identifies this tab to the hub's event stream (see event_bus.go) — a send
// this tab triggers is unicast back to just this id; one triggered elsewhere (e.g. a scheduled
// send) is broadcast to every session instead. New id per page load; nothing is persisted.
const joyousSessionId=(window.crypto&&crypto.randomUUID)?crypto.randomUUID():('sess-'+Date.now()+'-'+Math.random().toString(36).slice(2));

// sessionHeaders merges the X-Session-Id header (see requestSessionID in event_bus.go) into a
// fetch() headers object, for the send-triggering requests whose progress this tab wants
// unicast back to itself rather than broadcast to every open tab.
function sessionHeaders(extra){
  return Object.assign({'X-Session-Id':joyousSessionId}, extra||{});
}

// ── Toasts (top-right) ───────────────────────────────────────────────────────
const toastStack=document.getElementById('toast-stack');
const toastEls={}; // id -> element, so repeated updates (e.g. retry ticks) replace in place

function showToast(id,message,opts){
  opts=opts||{};
  let el=toastEls[id];
  if(!el){
    el=document.createElement('div');
    toastStack.appendChild(el);
    toastEls[id]=el;
  }
  clearTimeout(el._hideTimer);
  el.className='toast'+(opts.type?' toast-'+opts.type:'');
  el.textContent=message;
  if(opts.closable){
    const btn=document.createElement('button');
    btn.type='button';
    btn.className='toast-close';
    btn.textContent='×';
    btn.onclick=()=>dismissToast(id);
    el.appendChild(btn);
  }
  requestAnimationFrame(()=>el.classList.add('show'));
  if(opts.autoHideMs) el._hideTimer=setTimeout(()=>dismissToast(id),opts.autoHideMs);
}

function dismissToast(id){
  const el=toastEls[id];
  if(!el) return;
  clearTimeout(el._hideTimer);
  el.classList.remove('show');
  setTimeout(()=>{el.remove();delete toastEls[id];},200);
}

// applyBridgeStatus renders bridge nav badges from a "bridges" event pushed over the event
// stream (see connectJoyousEvents below) — the handshake snapshot on connect covers the
// initial paint, so there's no separate REST fetch for this anymore.
function applyBridgeStatus(bridges){
  bridges=bridges||[];
  const inkjoy=bridges.find(b=>b.id==='inkjoy'&&b.online&&b.has_config_ui);
  const wasOnline=inkjoyBridgeOnline;
  inkjoyBridgeOnline=!!inkjoy;
  const navIJ=document.getElementById('nav-inkjoy');
  const navMQ=document.getElementById('nav-mqtt');
  if(navIJ) navIJ.style.display=inkjoyBridgeOnline?'':'none';
  if(navMQ) navMQ.style.display='';
  if(!inkjoyBridgeOnline){
    const frame=document.getElementById('inkjoy-bridge-frame');
    if(frame){ frame.dataset.loaded=''; frame.removeAttribute('src'); }
  }
  if(!inkjoyBridgeOnline&&activeTab==='inkjoy'){
    showTab('devices',document.querySelector('nav button'));
  }
  if(inkjoyBridgeOnline&&activeTab==='inkjoy') loadInkjoyBridgeFrame();
  if(inkjoyBridgeOnline&&!wasOnline&&activeTab==='inkjoy') loadInkjoyBridgeFrame();

  const nixplay=bridges.find(b=>b.id==='nixplay'&&b.online&&b.has_config_ui);
  const nixplayWasOnline=nixplayBridgeOnline;
  nixplayBridgeOnline=!!nixplay;
  const navNX=document.getElementById('nav-nixplay');
  if(navNX) navNX.style.display=nixplayBridgeOnline?'':'none';
  if(!nixplayBridgeOnline){
    const frame=document.getElementById('nixplay-bridge-frame');
    if(frame){ frame.dataset.loaded=''; frame.removeAttribute('src'); }
  }
  if(!nixplayBridgeOnline&&activeTab==='nixplay'){
    showTab('devices',document.querySelector('nav button'));
  }
  if(nixplayBridgeOnline&&activeTab==='nixplay') loadNixplayBridgeFrame();
  if(nixplayBridgeOnline&&!nixplayWasOnline&&activeTab==='nixplay') loadNixplayBridgeFrame();
}

function loadInkjoyBridgeFrame(){
  const frame=document.getElementById('inkjoy-bridge-frame');
  if(!frame||frame.dataset.loaded==='1') return;
  frame.src='/inkjoy/';
  frame.dataset.loaded='1';
}

function loadNixplayBridgeFrame(){
  const frame=document.getElementById('nixplay-bridge-frame');
  if(!frame||frame.dataset.loaded==='1') return;
  frame.src='/nixplay/';
  frame.dataset.loaded='1';
}

function showTab(name,btn){
  activeTab=name;
  document.querySelectorAll('[id^=tab-]').forEach(e=>e.style.display='none');
  document.getElementById('tab-'+name).style.display='';
  document.querySelectorAll('nav button').forEach(b=>b.classList.remove('active'));
  if(btn) btn.classList.add('active');
  stopTabRefresh();
  if(name==='inkjoy'){ loadInkjoyBridgeFrame(); }
  else if(name==='nixplay'){ loadNixplayBridgeFrame(); }
  else if(name==='samsung'){ startTabRefresh(60000, refreshSamsungTab); startSamsungLogPoll(); }
  else if(name==='album') loadImages();
  else if(name==='overlays') loadOverlaysTab();
  else if(name==='color') loadColorTab();
  if(name==='mqtt') startMQTTLogPoll();
  else stopMQTTLogPoll();
  if(name!=='samsung') stopSamsungLogPoll();
}

