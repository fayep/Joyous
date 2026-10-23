// ── Samsung tab ─────────────────────────────────────────────────────────────
let samsungFrames=[], samsungCurrentId=null, samsungStatusCache=null, samsungPreviewKey=null;

function samsungOfflineLabel(d,rec,s){
  return samsungDeepSleep(d,rec,s)?'deep sleep':'asleep';
}

// InkJoy/Seeed shutdown fully ignores the network (radio off, RTC-timer
// wake) — the same "deep sleep" meaning used on the Samsung side (as
// opposed to "asleep" there, which means reachable network standby). It
// self-wakes on its own schedule with no remote/button nudge needed, so
// this is a label only — no restore push like Samsung's morning routine.
function inkjoyOfflineLabel(d){
  if(d.last_action==='shutdown' && d.sleep_end_time) return 'deep sleep · back '+d.sleep_end_time;
  return 'offline';
}

function samsungFrameRecord(frameId){
  return samsungFrames.find(x=>x.id===frameId);
}

function samsungDeviceForFrame(frameId){
  const rec=samsungFrameRecord(frameId);
  if(rec&&rec.device_id) return devices.find(d=>d.id===rec.device_id);
  return devices.find(d=>d.type==='samsung'&&samsungFrameIDFromDevice(d)===frameId);
}

function samsungFrameIDFromDevice(d){
  if(d.mdc_mac){
    const m=d.mdc_mac.replace(/[:\-\.]/g,'').toUpperCase();
    if(/^[0-9A-F]{12}$/.test(m)) return m;
  }
  if(d.mac){
    const m=d.mac.replace(/[:\-\.]/g,'').toUpperCase();
    if(/^[0-9A-F]{12}$/.test(m)) return m;
  }
  if(d.ip) return d.ip.replace(/\./g,'-');
  const id=d.id||'';
  if(id.startsWith('samsung:')){
    const suffix=id.slice(8);
    const m=suffix.replace(/[:\-\.]/g,'').toUpperCase();
    if(/^[0-9A-F]{12}$/.test(m)) return m;
    return suffix.replace(/\./g,'-');
  }
  return id.replace(/\./g,'-');
}

function samsungFrameListSig(frames){
  return frames.map(f=>f.id+'\x1f'+(f.name||f.ip||f.id)).join('\n');
}

function patchSamsungFrameListItem(f){
  const li=document.getElementById('samli-'+f.id);
  if(!li) return;
  li.classList.toggle('selected', f.id===samsungCurrentId);
  const dot=li.querySelector('.dot');
  if(dot) dot.className='dot '+(f.connected?'online':'offline');
  const label=f.name||f.ip||f.id;
  const title=li.querySelector('.sam-frame-label');
  if(title) title.textContent=label;
  let bat=li.querySelector('.sam-frame-battery');
  if(f.battery){
    if(!bat){
      bat=document.createElement('span');
      bat.className='sam-frame-battery';
      bat.style.cssText='margin-left:auto;font-size:.8rem;color:#666';
      li.appendChild(bat);
    }
    bat.textContent='🔋'+f.battery+'%'+(f.battery_push_delta!=null&&f.battery_samples>=2?(f.battery_push_delta===0?'':' '+(f.battery_push_delta>0?'+':'')+f.battery_push_delta+'%'):'');
  }else if(bat){
    bat.remove();
  }
}

function samsungBatteryMeta(obj){
  const pct=obj&&(obj.battery||(obj.battery===0?0:null));
  if(pct==null) return '—';
  const src=obj.power_source;
  let s=pct+'%'+(src?' · '+src:'');
  const pushDelta=obj.battery_push_delta;
  if(pushDelta!=null&&obj.battery_samples>=2){
    s+=(pushDelta===0?' · unchanged since last push':(' · '+(pushDelta>0?'+':'')+pushDelta+'% since last push'));
  }else if(obj.battery_delta!=null&&obj.battery_samples>=2){
    const d=obj.battery_delta;
    s+=(d===0?' · unchanged since last reading':(' · '+(d>0?'+':'')+d+'% since last reading'));
  }
  if(obj.battery_samples>1) s+=' · '+obj.battery_samples+' readings';
  return s;
}

function samsungBatteryHistoryHTML(history){
  if(!history||!history.length) return '';
  return history.slice().reverse().map(h=>{
    const when=h.at?timeAgo(h.at):'—';
    const src=h.source==='pre_sleep'?'push':(h.source||'');
    return '<div style="font-size:.8rem;color:#666">'+when+' · '+h.percent+'%'+(h.power_source?' · '+h.power_source:'')+(src?' · '+src:'')+'</div>';
  }).join('');
}

function samsungPreviewSpec(s, rec, d){
  // Prefer live list etag (PNG on hub disk) over status cache — status is only
  // fetched on open/reload and goes stale after album sends write a new PNG.
  const etag=(rec&&rec.etag)||(s&&s.etag)||'';
  const hasImage=(rec&&rec.has_image)||(s&&s.has_image);
  const locked=(s&&s.locked)||(rec&&rec.locked);
  if(d&&d.last_image_id){
    return 'album:'+d.last_image_id+':'+(d.last_overlay_hash||'')+':'+etag;
  }
  if(hasImage&&!locked) return 'img:'+etag;
  if(locked) return 'locked';
  return 'empty';
}

function samsungPreviewURL(frameId, key){
  return '/api/samsung/'+encodeURIComponent(frameId)+'/preview?v='+encodeURIComponent(key||'');
}

function samsungPreviewEmptyHTML(s, rec){
  const locked=(s&&s.locked)||(rec&&rec.locked);
  return '<p style="color:#888;font-size:.9rem">'+(locked?'Image locked.':'No image uploaded yet.')+'</p>';
}

async function loadSamsungFrames(){
  try{
    const r=await fetch('/api/samsung');
    samsungFrames=await r.json()||[];
  }catch(_){
    samsungFrames=[];
  }
  const el=document.getElementById('samsung-frame-list');
  if(!samsungFrames.length){
    el.innerHTML='<p style="color:#888;font-size:.9rem">No Samsung frames yet.</p>';
    el.dataset.samsungSig='';
    return;
  }
  const sig=samsungFrameListSig(samsungFrames);
  if(el.dataset.samsungSig!==sig){
    el.dataset.samsungSig=sig;
    el.innerHTML=samsungFrames.map(f=>{
      const label=f.name||f.ip||f.id;
      const sel=f.id===samsungCurrentId?' selected':'';
      const online=f.connected;
      return '<div class="frame-list-item'+sel+'" onclick="openSamsungFrame(\''+f.id+'\')" id="samli-'+f.id+'">'+
        '<div class="dot '+(online?'online':'offline')+'"></div>'+
        '<span class="sam-frame-label" style="font-weight:500">'+label+'</span>'+
        (f.battery?'<span class="sam-frame-battery" style="margin-left:auto;font-size:.8rem;color:#666">🔋'+f.battery+'%</span>':'')+
        '</div>';
    }).join('');
  }else{
    samsungFrames.forEach(patchSamsungFrameListItem);
  }
  if(samsungCurrentId){
    const d=samsungDeviceForFrame(samsungCurrentId);
    const rec=samsungFrameRecord(samsungCurrentId);
    if(rec||samsungStatusCache) updateSamsungEditorStatus(d,rec,samsungStatusCache);
  }
}

async function discoverSamsungFrames(){
  const btn=document.getElementById('samsung-discover-btn');
  const st=document.getElementById('samsung-discover-status');
  btn.disabled=true; st.textContent='Scanning network…';
  try{
    const r=await fetch('/api/devices/discover',{method:'POST'});
    const data=await r.json();
    if(!r.ok) throw new Error(data.error||r.statusText);
    st.textContent=data.found?'Found '+data.found+' frame(s)':'No frames matched';
    await loadDevicesInner();
    loadSamsungFrames();
  }catch(e){
    st.textContent='Discovery failed: '+e.message;
  }finally{
    btn.disabled=false;
  }
}

async function openSamsungFrame(frameId){
  samsungCurrentId=frameId;
  samsungPreviewKey=null;
  document.querySelectorAll('#samsung-frame-list .frame-list-item').forEach(el=>el.classList.remove('selected'));
  const li=document.getElementById('samli-'+frameId);
  if(li) li.classList.add('selected');
  document.getElementById('samsung-editor').style.display='';
  const d=samsungDeviceForFrame(frameId);
  try{
    const r=await fetch('/samsung/'+encodeURIComponent(frameId)+'/status');
    if(!r.ok) throw new Error(await r.text());
    samsungStatusCache=await r.json();
  }catch(e){
    alert('Load failed: '+e.message);
    return;
  }
  renderSamsungEditor(d,samsungStatusCache,samsungFrameRecord(frameId));
  samsungLoadDailyRefresh();
}

function samsungInactiveScheduleEnabled(begin,end){
  if(!begin||!end) return false;
  return begin.slice(0,5)!==end.slice(0,5);
}

function renderSamsungEditor(d,s,rec){
  updateSamsungEditorStatus(d,rec,s);
  document.getElementById('samsung-name-input').value=(s&&s.name)||(rec&&rec.name)||(d&&d.name)||'';
  document.getElementById('samsung-wifi-mac').value=(s&&s.wifi_mac)||(rec&&rec.wifi_mac)||(d&&d.mdc_mac)||'';
  document.getElementById('samsung-poll').value=(s&&s.poll_interval_minutes)||60;
  document.getElementById('samsung-inactive-begin').value=(s&&s.inactive_begin)||'';
  document.getElementById('samsung-inactive-end').value=(s&&s.inactive_end)||'';
  const dr=document.getElementById('samsung-daily-refresh');
  if(dr) dr.value=(s&&s.daily_refresh_time)||'';
  const drs=document.getElementById('samsung-daily-refresh-status');
  if(drs) drs.textContent='';
  const ods=document.getElementById('samsung-overnight-deep-sleep');
  if(ods) ods.checked=(s&&s.overnight_deep_sleep!==false);
  const fmt=(s&&s.crop_format)||'16:9';
  document.getElementById('samsung-portrait').checked=(fmt==='9:16'||fmt==='3:4');
  document.getElementById('samsung-display-width').value=(s&&s.display_width)||'';
  document.getElementById('samsung-display-height').value=(s&&s.display_height)||'';
  document.getElementById('samsung-auto-sleep').checked=(s&&s.auto_sleep_after_push!==false);
  document.getElementById('samsung-sleep-delay').value=(s&&s.sleep_after_push_seconds)||15;
}

function updateSamsungEditorStatus(d,rec,s){
  if(!d&&samsungCurrentId) d=samsungDeviceForFrame(samsungCurrentId);
  if(!rec&&samsungCurrentId) rec=samsungFrameRecord(samsungCurrentId);
  const label=(s&&s.name)||(rec&&rec.name)||(d&&d.name)||(rec&&rec.ip)||(d&&d.ip)||samsungCurrentId||'';
  document.getElementById('samsung-frame-title').textContent=label;
  const online=!!((d&&d.connected)||(rec&&rec.connected));
  document.getElementById('samsung-dot').className='dot '+(online?'online':'offline');
  const badge=document.getElementById('samsung-status-badge');
  badge.className='badge '+(online?'online':'offline');
  badge.textContent=online?'active':samsungOfflineLabel(d,rec,s);
  const wakeBtn=document.getElementById('samsung-wake-btn');
  const sleepBtn=document.getElementById('samsung-sleep-btn');
  if(wakeBtn) wakeBtn.style.display=online?'none':'inline-flex';
  if(sleepBtn) sleepBtn.style.display=online?'inline-flex':'none';
  const lastSeen=(d&&d.last_seen)||(rec&&rec.last_seen);
  const ago=lastSeen?timeAgo(lastSeen):'—';
  const ip=(d&&d.ip)||(rec&&rec.ip)||'—';
  const lastAction=(d&&d.last_action)||(rec&&rec.last_action)||'—';
  const batteryVal=(d&&d.battery)||(rec&&rec.battery);
  const batteryObj={
    battery:batteryVal,
    power_source:(d&&d.power_source)||(rec&&rec.power_source),
    battery_delta:(d&&d.battery_delta)!=null?d.battery_delta:(rec&&rec.battery_delta),
    battery_push_delta:(d&&d.battery_push_delta)!=null?d.battery_push_delta:(rec&&rec.battery_push_delta),
    battery_samples:(d&&d.battery_samples)||(rec&&rec.battery_samples)||0
  };
  const history=(d&&d.battery_history)||(rec&&rec.battery_history);
  document.getElementById('samsung-info').innerHTML=
    '<span class="label">Frame ID</span><span style="font-family:monospace">'+samsungCurrentId+'</span>'+
    '<span class="label">IP</span><span>'+ip+'</span>'+
    '<span class="label">Battery</span><span>'+samsungBatteryMeta(batteryObj)+'</span>'+
    (history&&history.length?('<span class="label">History</span><span>'+samsungBatteryHistoryHTML(history)+'</span>'):'')+
    '<span class="label">Crop</span><span>'+((s&&s.crop_format)||(rec&&rec.crop_format)||'—')+((s&&s.display_width)?(' · '+s.display_width+'×'+s.display_height):((rec&&rec.display_width)?(' · '+rec.display_width+'×'+rec.display_height):''))+'</span>'+
    '<span class="label">Poll</span><span>'+((s&&s.poll_interval_minutes)?(s.poll_interval_minutes+' min'):((rec&&rec.poll_interval_minutes)?(rec.poll_interval_minutes+' min'):'—'))+'</span>'+
    (s&&s.daily_refresh_time?('<span class="label">Daily refresh</span><span>'+s.daily_refresh_time+'</span>'):'')+
    '<span class="label">Last seen</span><span>'+ago+'</span>'+
    '<span class="label">Last action</span><span>'+lastAction+'</span>';
  const wrap=document.getElementById('samsung-preview-wrap');
  const key=samsungPreviewSpec(s,rec,d);
  if(key.startsWith('img:')||key.startsWith('album:')){
    const url=samsungPreviewURL(samsungCurrentId, key);
    const img=document.getElementById('samsung-preview');
    if(!img||img.parentElement!==wrap){
      wrap.innerHTML='<img class="last-image-preview" id="samsung-preview" src="'+url+'" alt="current image">';
      samsungPreviewKey=key;
    }else if(key!==samsungPreviewKey){
      samsungPreviewKey=key;
      refreshSamsungPreview(url);
    }
  }else if(key!==samsungPreviewKey){
    samsungPreviewKey=key;
    wrap.innerHTML=samsungPreviewEmptyHTML(s,rec);
  }
  const st=document.getElementById('samsung-status');
  const etag=(s&&s.etag)||(rec&&rec.etag);
  const hasImage=(s&&s.has_image)||(rec&&rec.has_image);
  const locked=(s&&s.locked)||(rec&&rec.locked);
  if(hasImage||s){
    st.textContent=(hasImage?'Image etag '+(etag||''):'No image yet')+(locked?' (locked)':'');
    if(samsungInactiveScheduleEnabled((s&&s.inactive_begin)||(rec&&rec.inactive_begin),(s&&s.inactive_end)||(rec&&rec.inactive_end))){
      st.textContent+=' · inactive '+((s&&s.inactive_begin)||(rec&&rec.inactive_begin))+'–'+((s&&s.inactive_end)||(rec&&rec.inactive_end));
    }
    if(samsungDeepSleep(d,rec,s||samsungStatusCache)) st.textContent+=' · deep sleep (button wake)';
  }else{
    st.textContent='';
  }
}

async function reloadSamsungFrame(){
  if(!samsungCurrentId) return;
  const d=samsungDeviceForFrame(samsungCurrentId);
  const rec=samsungFrameRecord(samsungCurrentId);
  try{
    const r=await fetch('/samsung/'+encodeURIComponent(samsungCurrentId)+'/status');
    if(!r.ok) throw new Error(await r.text());
    samsungStatusCache=await r.json();
    updateSamsungEditorStatus(d,rec,samsungStatusCache);
  }catch(_){}
  loadSamsungFrames();
}

function samsungConfigBody(extra){
  const begin=document.getElementById('samsung-inactive-begin').value;
  const end=document.getElementById('samsung-inactive-end').value;
  return Object.assign({
    name:document.getElementById('samsung-name-input').value.trim(),
    wifi_mac:document.getElementById('samsung-wifi-mac').value.trim(),
    poll_interval_minutes:parseInt(document.getElementById('samsung-poll').value,10)||60,
    inactive_begin:begin?begin.slice(0,5):'',
    inactive_end:end?end.slice(0,5):'',
    overnight_deep_sleep:document.getElementById('samsung-overnight-deep-sleep').checked,
    crop_format:samsungCropFormat(),
    display_width:parseInt(document.getElementById('samsung-display-width').value,10)||0,
    display_height:parseInt(document.getElementById('samsung-display-height').value,10)||0,
    auto_sleep_after_push:document.getElementById('samsung-auto-sleep').checked,
    sleep_after_push_seconds:parseInt(document.getElementById('samsung-sleep-delay').value,10)||15
  }, extra||{});
}

async function saveSamsungName(){
  if(!samsungCurrentId)return;
  const name=document.getElementById('samsung-name-input').value.trim();
  const r=await fetch('/samsung/'+encodeURIComponent(samsungCurrentId)+'/status');
  const s=await r.json();
  await fetch('/api/samsung/'+encodeURIComponent(samsungCurrentId)+'/config',{
    method:'PUT',headers:{'Content-Type':'application/json'},
    body:JSON.stringify(samsungConfigBody({
      poll_interval_minutes:s.poll_interval_minutes||60,
      inactive_begin:s.inactive_begin||'',
      inactive_end:s.inactive_end||'',
      overnight_deep_sleep:s.overnight_deep_sleep!==false,
      crop_format:s.crop_format||'16:9',
      display_width:s.display_width||0,
      display_height:s.display_height||0,
      auto_sleep_after_push:s.auto_sleep_after_push!==false,
      sleep_after_push_seconds:s.sleep_after_push_seconds||15
    }))
  });
  document.getElementById('samsung-frame-title').textContent=name||samsungCurrentId;
  await loadDevicesInner();
  loadSamsungFrames();
}

function samsungCropFormat(){
  const portrait=document.getElementById('samsung-portrait').checked;
  const w=parseInt(document.getElementById('samsung-display-width').value,10)||0;
  const h=parseInt(document.getElementById('samsung-display-height').value,10)||0;
  // Pick the right shorthand key for the aspect ratio so it matches saved crop rectangles.
  if(w>0&&h>0){
    const long=Math.max(w,h), short=Math.min(w,h), r=long/short;
    const is43=(r>=1.3&&r<1.4);
    if(portrait) return is43?'3:4':'9:16';
    return is43?'4:3':'16:9';
  }
  return portrait?'9:16':'16:9';
}

async function saveSamsungConfig(){
  if(!samsungCurrentId)return;
  const r=await fetch('/api/samsung/'+encodeURIComponent(samsungCurrentId)+'/config',{
    method:'PUT',headers:{'Content-Type':'application/json'},
    body:JSON.stringify(samsungConfigBody())
  });
  if(!r.ok){alert('Save failed: '+(await r.text()));return;}
  await reloadSamsungFrame();
  await loadDevicesInner();
}

async function samsungLoadDailyRefresh(){
  if(!samsungCurrentId) return;
  const st=document.getElementById('samsung-daily-refresh-status');
  try{
    const r=await fetch('/api/samsung/'+encodeURIComponent(samsungCurrentId)+'/daily-refresh');
    if(!r.ok) throw new Error(await r.text());
    const j=await r.json();
    const dr=document.getElementById('samsung-daily-refresh');
    if(dr&&j.daily_refresh_time) dr.value=j.daily_refresh_time;
    if(st) st.textContent=j.query_error?('query: '+j.query_error):'';
    if(samsungStatusCache) samsungStatusCache.daily_refresh_time=j.daily_refresh_time||'';
  }catch(e){
    if(st) st.textContent='load failed';
  }
}

async function samsungSyncDailyRefresh(){
  if(!samsungCurrentId) return;
  const st=document.getElementById('samsung-daily-refresh-status');
  if(st) st.textContent='Syncing…';
  try{
    const r=await fetch('/api/samsung/'+encodeURIComponent(samsungCurrentId)+'/daily-refresh/sync-inactive',{method:'POST'});
    if(!r.ok) throw new Error(await r.text());
    const j=await r.json();
    const dr=document.getElementById('samsung-daily-refresh');
    if(dr&&j.daily_refresh_time) dr.value=j.daily_refresh_time;
    if(st) st.textContent='Synced to inactive end';
    if(samsungStatusCache) samsungStatusCache.daily_refresh_time=j.daily_refresh_time||'';
    await reloadSamsungFrame();
  }catch(e){
    alert('Sync failed: '+e.message);
    if(st) st.textContent='';
  }
}

async function samsungWake(){
  if(!samsungCurrentId)return;
  const st=document.getElementById('samsung-status');
  st.textContent='Waking display (may take up to ~45s)…';
  try{
    const r=await fetch('/api/samsung/'+encodeURIComponent(samsungCurrentId)+'/wake',{method:'POST'});
    if(!r.ok) throw new Error(await r.text());
    st.textContent='Wake requested — watch handshake log / status';
    // Bridge runs wake async (WoL + MDC wait up to ~45s). Poll until active.
    for(let i=0;i<16;i++){
      await new Promise(res=>setTimeout(res,3000));
      await loadDevicesInner();
      await reloadSamsungFrame();
      const d=samsungDeviceForFrame(samsungCurrentId);
      const rec=samsungFrameRecord(samsungCurrentId);
      if((d&&d.connected)||(rec&&rec.connected)){
        st.textContent='Wake ok — active';
        return;
      }
    }
    st.textContent='Wake sent; frame still asleep — check WiFi MAC / Network Standby';
  }catch(e){
    alert('Wake failed: '+e.message);
    st.textContent='';
  }
}

async function samsungSleep(){
  if(!samsungCurrentId)return;
  const st=document.getElementById('samsung-status');
  st.textContent='Sending sleep…';
  try{
    const r=await fetch('/api/samsung/'+encodeURIComponent(samsungCurrentId)+'/sleep',{method:'POST'});
    if(!r.ok) throw new Error(await r.text());
    await loadDevicesInner();
    await reloadSamsungFrame();
    st.textContent='Sleep sent';
  }catch(e){
    alert('Sleep failed: '+e.message);
    st.textContent='';
  }
}

async function samsungPushCurrent(){
  if(!samsungCurrentId)return;
  const rec=samsungFrameRecord(samsungCurrentId);
  const dev=samsungDeviceForFrame(samsungCurrentId);
  if(!rec&&!dev){alert('Frame is not registered on the hub — click Discover displays first.');return;}
  const st=document.getElementById('samsung-status');
  const deepHint=samsungDeepSleepWakeHint(dev,rec,samsungStatusCache);
  if(st) st.textContent=deepHint||'Pushing…';
  try{
    const r=await fetch('/api/samsung/'+encodeURIComponent(samsungCurrentId)+'/push',{method:'POST',headers:sessionHeaders()});
    if(!r.ok) throw new Error(await r.text());
    const j=await r.json();
    let delivered=false;
    try{ delivered=j.send_id?await watchSendProgress(j.send_id,dev):false; }catch(_){}
    if(document.getElementById('samsung-auto-sleep').checked){
      const delay=parseInt(document.getElementById('samsung-sleep-delay').value,10)||15;
      if(st) st.textContent=delivered?('✓ Pushed — frame will sleep in ~'+delay+'s…'):'Pushed (unconfirmed)';
    }else if(st){
      st.textContent=delivered?'✓ Pushed':'Pushed (unconfirmed)';
    }
    await loadDevicesInner();
    loadSamsungFrames();
    await reloadSamsungFrame();
  }catch(e){
    alert('Push failed: '+e.message);
    if(st) st.textContent='';
  }
}

async function samsungDeleteDevice(){
  if(!samsungCurrentId)return;
  const rec=samsungFrameRecord(samsungCurrentId);
  const dev=samsungDeviceForFrame(samsungCurrentId);
  const deviceId=(rec&&rec.device_id)||(dev&&dev.id);
  if(!deviceId){alert('This frame is not in the device registry.');return;}
  if(!confirm('Remove this frame from the hub? (The display itself is unaffected.)'))return;
  await fetch('/api/devices/'+encodeURIComponent(deviceId),{method:'DELETE'});
  samsungCurrentId=null;
  samsungStatusCache=null;
  samsungPreviewKey=null;
  document.getElementById('samsung-editor').style.display='none';
  await loadDevicesInner();
  loadSamsungFrames();
}

document.getElementById('samsung-file-input').addEventListener('change',async e=>{
  if(!samsungCurrentId||!e.target.files.length)return;
  const fd=new FormData(); fd.append('file',e.target.files[0]);
  const r=await fetch('/api/samsung/'+encodeURIComponent(samsungCurrentId)+'/image',{method:'POST',body:fd});
  if(!r.ok){alert('Upload failed: '+(await r.text()));return;}
  await reloadSamsungFrame();
});
const sz=document.getElementById('samsung-upload-zone');
sz.addEventListener('dragover',e=>{e.preventDefault();sz.style.borderColor='#1a1a2e'});
sz.addEventListener('dragleave',()=>{sz.style.borderColor='#ccc'});
sz.addEventListener('drop',async e=>{
  e.preventDefault();sz.style.borderColor='#ccc';
  if(!samsungCurrentId||!e.dataTransfer.files.length)return;
  const fd=new FormData(); fd.append('file',e.dataTransfer.files[0]);
  const r=await fetch('/api/samsung/'+encodeURIComponent(samsungCurrentId)+'/image',{method:'POST',body:fd});
  if(!r.ok){alert('Upload failed: '+(await r.text()));return;}
  await reloadSamsungFrame();
});
