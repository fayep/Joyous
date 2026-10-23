// ── InkJoy tab ───────────────────────────────────────────────────────────────
let ijDevices=[], ijCurrentId=null, ijPreviewKey=null;

function ijHasDisplayPreview(d){
  return !!d.display_preview_at&&!d.display_preview_at.startsWith('0001-');
}

function ijPreviewSpec(d){
  if(d.last_image_id) return 'album:'+d.last_image_id;
  if(ijHasDisplayPreview(d)) return 'ext:'+d.display_preview_at;
  return '';
}

function ijPreviewURL(d){
  if(d.last_image_id){
    const q=new URLSearchParams();
    if(d.portrait) q.set('portrait','1');
    if(d.last_overlay_hash) q.set('overlay', d.last_overlay_hash);
    const qs=q.toString();
    return '/images/'+encodeURIComponent(d.last_image_id)+'/frame-preview'+(qs?'?'+qs:'');
  }
  if(ijHasDisplayPreview(d)){
    return '/api/devices/'+encodeURIComponent(d.id)+'/display-preview?t='+encodeURIComponent(d.display_preview_at);
  }
  return '';
}

function ijFrameListSig(inkjoy){
  return inkjoy.map(d=>d.id+'\x1f'+(d.name||d.mac||d.id)).join('\n');
}

function patchIJFrameListItem(d){
  const li=document.getElementById('ijli-'+d.id);
  if(!li) return;
  li.classList.toggle('selected', d.id===ijCurrentId);
  const dot=li.querySelector('.dot');
  if(dot) dot.className='dot '+(d.connected?'online':'offline');
  const label=d.name||d.mac||d.id;
  const title=li.querySelector('.ij-frame-label');
  if(title) title.textContent=label;
  let bat=li.querySelector('.ij-frame-battery');
  if(d.battery){
    if(!bat){
      bat=document.createElement('span');
      bat.className='ij-frame-battery';
      bat.style.cssText='margin-left:auto;font-size:.8rem;color:#666';
      li.appendChild(bat);
    }
    bat.textContent='🔋'+d.battery+'%';
  }else if(bat){
    bat.remove();
  }
}

function loadIJFrames(){
  const inkjoy=devices.filter(d=>d.type==='inkjoy');
  ijDevices=inkjoy;
  const el=document.getElementById('inkjoy-frame-list');
  if(!inkjoy.length){
    el.innerHTML='<p style="color:#888;font-size:.9rem">No InkJoy frames yet.</p>';
    el.dataset.ijSig='';
    return;
  }
  const sig=ijFrameListSig(inkjoy);
  if(el.dataset.ijSig!==sig){
    el.dataset.ijSig=sig;
    el.innerHTML=inkjoy.map(d=>{
      const label=d.name||d.mac||d.id;
      const sel=d.id===ijCurrentId?' selected':'';
      const online=d.connected;
      return '<div class="frame-list-item'+sel+'" onclick="openIJFrame(\''+d.id+'\')" id="ijli-'+d.id+'">'+
        '<div class="dot '+(online?'online':'offline')+'"></div>'+
        '<span class="ij-frame-label" style="font-weight:500">'+label+'</span>'+
        (d.battery?'<span class="ij-frame-battery" style="margin-left:auto;font-size:.8rem;color:#666">🔋'+d.battery+'%</span>':'')+
        '</div>';
    }).join('');
  }else{
    inkjoy.forEach(patchIJFrameListItem);
  }
  // On periodic refresh, only update status/info — never form inputs.
  if(ijCurrentId){
    const d=ijDevices.find(x=>x.id===ijCurrentId);
    if(d) updateIJEditorStatus(d);
  }
}

function openIJFrame(id){
  ijCurrentId=id;
  ijPreviewKey=null;
  document.querySelectorAll('.frame-list-item').forEach(el=>el.classList.remove('selected'));
  const li=document.getElementById('ijli-'+id);
  if(li)li.classList.add('selected');
  const d=ijDevices.find(x=>x.id===id);
  if(d) renderIJEditor(d);  // full render including form inputs
  document.getElementById('inkjoy-editor').style.display='';
}

// renderIJEditor does a full render including form inputs — call only when opening a frame.
function renderIJEditor(d){
  updateIJEditorStatus(d);
  document.getElementById('ij-name-input').value=d.name||'';
  document.getElementById('ij-portrait').checked=!!d.portrait;
  document.getElementById('ij-sleep-begin').value=d.sleep_begin_time||'';
  document.getElementById('ij-sleep-end').value=d.sleep_end_time||'';
}

// updateIJEditorStatus refreshes only the read-only parts — safe to call on every poll.
function updateIJEditorStatus(d){
  const label=d.name||d.mac||d.id;
  document.getElementById('ij-title').textContent=label;
  const dot=document.getElementById('ij-dot');
  dot.className='dot '+(d.connected?'online':'offline');
  const badge=document.getElementById('ij-status-badge');
  badge.className='badge '+(d.connected?'online':'offline');
  badge.textContent=d.connected?'online':inkjoyOfflineLabel(d);
  const ago=d.last_seen?timeAgo(d.last_seen):'never';
  document.getElementById('ij-info').innerHTML=
    '<span class="label">MAC</span><span style="font-family:monospace">'+d.mac+'</span>'+
    '<span class="label">Firmware</span><span>'+(d.firmware||'—')+'</span>'+
    '<span class="label">Battery</span><span>'+(d.battery?d.battery+'%':'—')+'</span>'+
    '<span class="label">Signal</span><span>'+(d.rssi?d.rssi+' dBm':'—')+'</span>'+
    '<span class="label">Last seen</span><span>'+ago+'</span>'+
    '<span class="label">Last action</span><span>'+(d.last_action||'—')+'</span>';
  const li=document.getElementById('ij-last-image');
  const key=ijPreviewSpec(d);
  const url=ijPreviewURL(d);
  if(!key){
    if(ijPreviewKey!==null){
      ijPreviewKey=null;
      li.innerHTML='<p style="color:#888;font-size:.9rem">Nothing sent yet.</p>';
    }
    return;
  }
  const img=document.getElementById('ij-preview');
  if(!img||img.parentElement!==li){
    li.innerHTML='<img class="last-image-preview" id="ij-preview" src="'+url+'" alt="currently displayed">';
    ijPreviewKey=key;
  }else if(key!==ijPreviewKey){
    ijPreviewKey=key;
    refreshIJPreview(url);
  }
}

function timeAgo(iso){
  const d=new Date(iso), now=Date.now(), s=Math.round((now-d)/1000);
  if(s<5)return 'just now';
  if(s<60)return s+'s ago';
  if(s<3600)return Math.round(s/60)+'m ago';
  if(s<86400)return Math.round(s/3600)+'h ago';
  return Math.round(s/86400)+'d ago';
}

async function saveIJName(){
  if(!ijCurrentId)return;
  const name=document.getElementById('ij-name-input').value.trim();
  await fetch('/api/devices/'+encodeURIComponent(ijCurrentId),{
    method:'PATCH',headers:{'Content-Type':'application/json'},
    body:JSON.stringify({name})
  });
  await loadDevicesInner();
  loadIJFrames();
}

async function saveIJPortrait(){
  if(!ijCurrentId)return;
  const portrait=document.getElementById('ij-portrait').checked;
  await fetch('/api/devices/'+encodeURIComponent(ijCurrentId),{
    method:'PATCH',headers:{'Content-Type':'application/json'},
    body:JSON.stringify({portrait})
  });
}

async function ijRefresh(){
  if(!ijCurrentId)return;
  await fetch('/api/devices/'+encodeURIComponent(ijCurrentId)+'/refresh',{method:'POST'});
}

async function ijSaveSleep(){
  if(!ijCurrentId)return;
  const begin=document.getElementById('ij-sleep-begin').value;
  const end=document.getElementById('ij-sleep-end').value;
  const r=await fetch('/api/devices/'+encodeURIComponent(ijCurrentId)+'/sleep',{
    method:'POST',headers:{'Content-Type':'application/json'},
    body:JSON.stringify({beginTime:begin||'00:00',endTime:end||'00:00',mode:2})
  });
  if(!r.ok)alert('Error: '+(await r.text()));
}

async function ijClearSleep(){
  if(!ijCurrentId)return;
  document.getElementById('ij-sleep-begin').value='00:00';
  document.getElementById('ij-sleep-end').value='00:00';
  await fetch('/api/devices/'+encodeURIComponent(ijCurrentId)+'/sleep',{
    method:'POST',headers:{'Content-Type':'application/json'},
    body:JSON.stringify({beginTime:'00:00',endTime:'00:00',mode:2})
  });
}

async function ijDeleteDevice(){
  if(!ijCurrentId)return;
  if(!confirm('Remove this frame from the hub? (The frame itself is unaffected.)'))return;
  await fetch('/api/devices/'+encodeURIComponent(ijCurrentId),{method:'DELETE'});
  ijCurrentId=null;
  document.getElementById('inkjoy-editor').style.display='none';
  await loadDevicesInner();
  loadIJFrames();
}

function deviceStatusHTML(d){
  const type=d.type||'inkjoy';
  return type==='samsung'
    ? (d.connected?'<span class="badge online">active</span>':'<span class="badge offline">'+samsungOfflineLabel(d)+'</span>')
    : (d.connected?'<span class="badge online">online</span>':'<span class="badge offline">'+inkjoyOfflineLabel(d)+'</span>');
}

function deviceMetaHTML(d){
  const type=d.type||'inkjoy';
  return type==='inkjoy'
    ? ((d.firmware?'fw '+d.firmware+' ':'')+(d.battery?'🔋'+d.battery+'% ':'')+(d.rssi?'📶'+d.rssi+'dBm ':''))
    : (d.ip?d.ip+' ':'')+(d.battery?'🔋'+d.battery+'% ':'')+(d.power_source?('<span style="color:#666">'+d.power_source+' </span>'):'')+(d.display_crop_format?('<span style="color:#666">'+d.display_crop_format+(d.display_width?(' · '+d.display_width+'×'+d.display_height):'')+'</span> '):'')+(d.usn?'<span style="color:#888;font-size:.8rem">'+d.usn.split('::')[0]+'</span>':'');
}

// deviceLabel is the display-name fallback chain shared by initial card render and
// the poll-driven patch below, so both paths can never drift out of sync.
function deviceLabel(d){
  return d.name||d.mac||d.ip||d.id;
}

function deviceCardHTML(d){
  const label=escHtml(deviceLabel(d));
  const type=d.type||'inkjoy';
  const refreshBtn=type==='inkjoy'?'<button class="btn btn-sm btn-primary" onclick="refreshDevice(\''+d.id+'\')">Refresh display</button> ':'';
  const colorCalBtn=(type==='inkjoy'||type==='samsung')?'<button class="btn btn-sm" onclick="toggleColorCal(\''+d.id+'\')">Color calibration</button>':'';
  return '<div class="card" id="card-'+d.id+'" data-device-id="'+d.id+'">'+
    '<span class="badge" style="background:#eee;color:#333;margin-right:.5rem">'+type+'</span>'+
    '<strong id="label-'+d.id+'">'+label+'</strong> '+
    '<span id="status-'+d.id+'">'+deviceStatusHTML(d)+'</span> '+
    '<span id="meta-'+d.id+'" style="margin-left:.5rem;color:#666;font-size:.9rem">'+deviceMetaHTML(d)+'</span>'+
    '<div style="margin-top:.5rem">'+refreshBtn+
    '<button class="btn btn-sm btn-primary" onclick="sendToFrame(\''+d.id+'\')">Send image</button> '+
    '<button class="btn btn-sm" onclick="toggleSchedule(\''+d.id+'\')">Scheduled sends</button> '+
    colorCalBtn+
    '</div>'+
    '<div id="sched-'+d.id+'" style="display:none;margin-top:.6rem;padding-top:.6rem;border-top:1px solid #eee"></div>'+
    '<div id="colorcal-'+d.id+'" style="display:none;margin-top:.6rem;padding-top:.6rem;border-top:1px solid #eee"></div>'+
    '</div>';
}

// loadDevicesInner patches the existing card for each still-present device in place
// (label/status/meta only) rather than replacing the whole #device-list innerHTML, so an
// open "Scheduled sends" editor — or any other in-progress interaction inside a card — isn't
// destroyed by the 5s poll. Cards are only fully added/removed when a device appears/disappears;
// devices is already server-sorted, so re-appending each card in that order (appendChild moves
// an existing node rather than recreating it) keeps on-screen order in sync as devices are
// added/renamed, without disturbing any card's contents or open editor.
// applyDevicesSnapshot renders a full device list, from either a direct fetch
// (loadDevicesInner) or a live "devices" event pushed over the event stream (see
// joyousEvents below) — same incremental-patch logic either way.
function applyDevicesSnapshot(newDevices){
  devices=newDevices||[];
  const el=document.getElementById('device-list');
  if(!devices.length){el.innerHTML='<p>No frames yet. Connect an InkJoy frame via MQTT, or click Discover for Samsung displays.</p>';return;}
  if(!el.querySelector('.card')) el.innerHTML=''; // coming from the empty-state message or first load
  const seen=new Set();
  devices.forEach(d=>{
    seen.add(d.id);
    let card=document.getElementById('card-'+d.id);
    if(!card){
      el.insertAdjacentHTML('beforeend', deviceCardHTML(d));
      card=document.getElementById('card-'+d.id);
    }else{
      const labelEl=document.getElementById('label-'+d.id);
      if(labelEl) labelEl.textContent=deviceLabel(d);
      const statusEl=document.getElementById('status-'+d.id);
      if(statusEl) statusEl.innerHTML=deviceStatusHTML(d);
      const metaEl=document.getElementById('meta-'+d.id);
      if(metaEl) metaEl.innerHTML=deviceMetaHTML(d);
    }
    if(card) el.appendChild(card);
  });
  el.querySelectorAll('.card[data-device-id]').forEach(card=>{
    if(!seen.has(card.dataset.deviceId)) card.remove();
  });
  // Album sends update last_image_id here; refresh Samsung "currently displayed"
  // from hub PNG etag without waiting for the 60s tab poll.
  if(activeTab==='samsung') loadSamsungFrames();
}

async function loadDevicesInner(){
  const r=await fetch('/api/devices');
  applyDevicesSnapshot(await r.json());
}

