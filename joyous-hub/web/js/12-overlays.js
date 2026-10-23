// ── Overlays tab ────────────────────────────────────────────────────────────
let overlayConfig=null;
let overlayPreviewTimer=null;

function debouncedOverlayPreview(){
  clearTimeout(overlayPreviewTimer);
  overlayPreviewTimer=setTimeout(()=>{
    refreshOverlayMetrics();
    refreshOverlayPreview();
  }, 400);
}

function renderOverlayMetricsBlock(label, m){
  if(m.error){
    return '<p style="color:#c00;margin:.35rem 0">'+esc(label)+': '+esc(m.error)+'</p>';
  }
  const lines=(m.lines||[]).map(ln=>
    '<div style="margin:.35rem 0;padding:.4rem .55rem;background:#f6f7f9;border:1px solid #e8eaed;border-radius:4px;font-family:ui-monospace,Menlo,monospace;font-size:.75rem;line-height:1.35">'+
    '<div style="color:#666;margin-bottom:.15rem">Line '+ln.index+' · '+ln.font_size+'px · ~'+ln.width_px+' px wide · +'+ln.step_px+' px tall</div>'+
    '<div style="color:#222">'+esc(ln.text)+'</div></div>'
  ).join('');
  return '<div style="margin-top:.65rem"><div style="font-weight:600;color:#444;margin-bottom:.25rem">'+esc(label)+'</div>'+
    lines+
    '<div style="margin-top:.55rem;padding:.45rem .55rem;background:#fff;border:1px dashed #ccc;border-radius:4px;line-height:1.5;color:#444">'+
    '<div><b>Content</b> (max line × sum of line heights): ~'+m.content.width_px+' × '+m.content.height_px+' px</div>'+
    (m.style==='outline'
      ? '<div><b>Style</b> white bordered text with light shadow (no background panel)</div>'
      : '<div><b>Box</b> (+'+m.box.border_px+' px border each side): '+m.box.width_px+' × '+m.box.height_px+' px</div>')+
    '</div></div>';
}

function renderOverlayMetrics(resp){
  const el=document.getElementById('ovl-template-metrics');
  if(!el) return;
  el.innerHTML=
    '<div style="margin-bottom:.35rem;color:#666">Rendered lines (approximate, using live weather). Font sizes scale by display diagonal so physical size matches across panels.</div>'+
    renderOverlayMetricsBlock('Samsung (2560×1440)', resp.samsung||{})+
    renderOverlayMetricsBlock('InkJoy (1600×1200)', resp.inkjoy||{});
}

async function refreshOverlayMetrics(){
  const el=document.getElementById('ovl-template-metrics');
  if(!el) return;
  try{
    const portrait=!!document.getElementById('ovl-preview-portrait')?.checked;
    const r=await fetch('/api/overlay/metrics',{
      method:'POST',
      headers:{'Content-Type':'application/json'},
      body:JSON.stringify({config:overlayFormValues(), portrait})
    });
    if(!r.ok) throw new Error(await r.text());
    renderOverlayMetrics(await r.json());
  }catch(e){
    el.innerHTML='<p style="color:#c00;margin:0">Size estimate failed: '+esc(e.message)+'</p>';
  }
}

async function loadOverlayConfig(){
  const r=await fetch('/api/overlay');
  if(!r.ok) throw new Error(await r.text());
  overlayConfig=await r.json();
}

function applyOverlayForm(cfg){
  document.getElementById('ovl-enabled').checked=!!cfg.enabled;
  document.getElementById('ovl-location').value=cfg.location||'';
  document.getElementById('ovl-lat').value=cfg.latitude||'';
  document.getElementById('ovl-lon').value=cfg.longitude||'';
  document.getElementById('ovl-timezone').value=cfg.timezone||'';
  document.getElementById('ovl-template').value=cfg.template||'';
  document.getElementById('ovl-weather-style').value=cfg.weather_style||'box';
  document.getElementById('ovl-fahrenheit').checked=cfg.use_fahrenheit!==false;
  document.getElementById('ovl-date-style').value=String(cfg.date_style||1);
  document.getElementById('ovl-show-photo-name').checked=!!cfg.show_photo_name;
  document.getElementById('ovl-photo-name-position').value=cfg.photo_name_position||'bottom_right';
}

function overlayFormValues(){
  const lat=parseFloat(document.getElementById('ovl-lat').value);
  const lon=parseFloat(document.getElementById('ovl-lon').value);
  return {
    enabled: document.getElementById('ovl-enabled').checked,
    layout: 'bottom_bar',
    location: document.getElementById('ovl-location').value.trim(),
    latitude: Number.isFinite(lat)?lat:0,
    longitude: Number.isFinite(lon)?lon:0,
    timezone: document.getElementById('ovl-timezone').value.trim(),
    template: document.getElementById('ovl-template').value,
    weather_style: document.getElementById('ovl-weather-style').value,
    use_fahrenheit: document.getElementById('ovl-fahrenheit').checked,
    date_style: parseInt(document.getElementById('ovl-date-style').value,10)||1,
    show_photo_name: document.getElementById('ovl-show-photo-name').checked,
    photo_name_position: document.getElementById('ovl-photo-name-position').value
  };
}

async function saveOverlayConfig(){
  const st=document.getElementById('ovl-save-status');
  st.textContent='Saving…';
  try{
    const r=await fetch('/api/overlay',{
      method:'PUT',
      headers:{'Content-Type':'application/json'},
      body:JSON.stringify(overlayFormValues())
    });
    if(!r.ok) throw new Error(await r.text());
    overlayConfig=overlayFormValues();
    st.style.color='#28a745';
    st.textContent='Saved.';
    refreshOverlayPreview();
    renderOverlaySendList();
  }catch(e){
    st.style.color='#dc3545';
    st.textContent='Save failed: '+e.message;
  }
}

function overlayPreviewImageId(){
  const sel=document.getElementById('ovl-preview-image');
  return sel&&sel.value?sel.value:'';
}

async function refreshOverlayPreview(){
  const wrap=document.getElementById('ovl-preview-wrap');
  const imageId=overlayPreviewImageId();
  if(!imageId){
    wrap.innerHTML='<p style="color:#888;font-size:.9rem">Upload images to the album first.</p>';
    return;
  }
  const portrait=document.getElementById('ovl-preview-portrait').checked;
  wrap.innerHTML='<p style="color:#888;font-size:.9rem">Loading preview…</p>';
  try{
    const r=await fetch('/api/overlay/preview',{
      method:'POST',
      headers:{'Content-Type':'application/json'},
      body:JSON.stringify({
        image_id:imageId,
        portrait:portrait,
        config:overlayFormValues()
      })
    });
    if(!r.ok) throw new Error(await r.text());
    const blob=await r.blob();
    const url=URL.createObjectURL(blob);
    wrap.innerHTML='<img class="last-image-preview" src="'+url+'" alt="overlay preview">';
  }catch(e){
    wrap.innerHTML='<p style="color:#c00;font-size:.9rem">Preview failed: '+esc(e.message)+'</p>';
  }
}

function overlaySendImageForDevice(d){
  return d.last_image_id||'';
}

function renderOverlaySendList(){
  const el=document.getElementById('ovl-send-list');
  const frames=devices.filter(d=>d.type==='inkjoy'||d.type==='samsung');
  if(!frames.length){
    el.innerHTML='<p style="color:#888;font-size:.9rem">No frames registered yet.</p>';
    return;
  }
  el.innerHTML=frames.map(d=>{
    const label=d.name||d.mac||d.id;
    const src=overlaySendImageForDevice(d);
    const hint=src?'current frame image':'no tracked image — send from Album first';
    return '<div style="display:flex;align-items:center;gap:.75rem;margin:.5rem 0;flex-wrap:wrap">'+
      '<span class="badge" style="background:#eee;color:#333">'+d.type+'</span>'+
      '<strong>'+esc(label)+'</strong>'+
      '<span style="color:#888;font-size:.85rem">'+esc(hint)+'</span>'+
      '<button class="btn btn-sm btn-primary" onclick="overlaySend(\''+d.id+'\')"'+(!src?' disabled':'')+'>Send with overlay</button>'+
      '</div>';
  }).join('');
}

async function overlaySend(deviceId){
  await loadDevicesInner();
  const d=devices.find(x=>x.id===deviceId);
  if(!d||!d.last_image_id){
    alert('No hub-tracked image on this frame. Send a photo from Album first (overlay will apply automatically when enabled).');
    return;
  }
  if(!overlayConfig||!overlayConfig.enabled){
    alert('Enable overlay in settings first.');
    return;
  }
  const btn=event&&event.target;
  const orig=btn?btn.textContent:'';
  if(btn){btn.disabled=true;btn.textContent='Sending…';}
  try{
    const r=await fetch('/api/overlay/send',{
      method:'POST',
      headers:sessionHeaders({'Content-Type':'application/json'}),
      body:JSON.stringify({device_id:deviceId,use_current:true})
    });
    if(!r.ok) throw new Error(await r.text());
    const j=await r.json();
    if(btn&&d?.type!=='samsung') btn.textContent='Downloading…';
    let delivered=false;
    try{ delivered=j.send_id?await watchSendProgress(j.send_id,d):false; }catch(_){}
    if(btn){
      btn.textContent=delivered?'✓ Sent':'Sent (unconfirmed)';
      setTimeout(()=>{btn.textContent=orig;btn.disabled=false;},2000);
    }
    await loadDevicesInner();
    if(activeTab==='overlays') renderOverlaySendList();
  }catch(e){
    alert('Overlay send failed: '+e.message);
    if(btn){btn.textContent=orig;btn.disabled=false;}
  }
}

async function loadOverlaysTab(){
  try{
    if(!overlayConfig) await loadOverlayConfig();
    applyOverlayForm(overlayConfig||{});
    if(!images.length) await loadImages();
    const sel=document.getElementById('ovl-preview-image');
    sel.innerHTML=images.map(i=>'<option value="'+i.id+'">'+esc(i.name||i.id)+'</option>').join('');
    if(!sel.value&&images.length) sel.selectedIndex=0;
    renderOverlaySendList();
    refreshOverlayMetrics();
    refreshOverlayPreview();
  }catch(e){
    document.getElementById('ovl-save-status').textContent='Load failed: '+e.message;
  }
}

