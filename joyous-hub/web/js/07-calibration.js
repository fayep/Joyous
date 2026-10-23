// ── Calibration chart ────────────────────────────────────────────────────────
let calibrationKind=null;

function openCalibrationModal(kind){
  calibrationKind=kind;
  const modal=document.getElementById('calibration-modal');
  const title=document.getElementById('calibration-modal-title');
  const desc=document.getElementById('calibration-modal-desc');
  const img=document.getElementById('calibration-modal-img');
  const st=document.getElementById('calibration-send-status');
  const sendBtn=document.getElementById('calibration-send-btn');
  if(kind==='inkjoy'){
    title.textContent='InkJoy calibration chart';
    desc.textContent='Six P1 primary swatches (1600×1200). Vertical-sweep wipe on non-green inks; green pixels use lo=248. Photograph for P2 palette tuning.';
    sendBtn.disabled=!ijCurrentId;
    img.src='/api/calibration/inkjoy?v=2';
  }else if(kind==='inkjoy-lo-ladder'){
    title.textContent='InkJoy lo ladder chart';
    desc.textContent='Two plays from the browser: (1) full-screen black at lo=248, wait for refresh + 90s settle, then (2) 8×4 lo ladder boxes. Color-tab wipe is ignored.';
    sendBtn.disabled=!ijCurrentId;
    img.src='/api/calibration/inkjoy-lo-ladder?v=1';
  }else{
    title.textContent='Samsung calibration chart';
    desc.textContent='Six P1 primary swatches (2560×1440). Send flat to the selected frame, then photograph for P2 palette tuning.';
    sendBtn.disabled=!samsungCurrentId;
    img.src='/api/calibration/'+kind+'?v=1';
  }
  if(st) st.textContent='';
  modal.style.display='flex';
}

function closeCalibrationModal(){
  const modal=document.getElementById('calibration-modal');
  modal.style.display='none';
  calibrationKind=null;
}

async function sendCalibrationChart(){
  const st=document.getElementById('calibration-send-status');
  const btn=document.getElementById('calibration-send-btn');
  if(!calibrationKind){return;}
  if(calibrationKind==='inkjoy'){
    if(!ijCurrentId){alert('Select an InkJoy frame first.');return;}
    if(st) st.textContent='Sending primaries…';
    if(btn) btn.disabled=true;
    try{
      const j=await readJSONResponse(await fetch('/api/calibration/inkjoy/send',{
        method:'POST',
        headers:sessionHeaders({'Content-Type':'application/json'}),
        body:JSON.stringify({device_id:ijCurrentId})
      }));
      const delivered=j.send_id?await waitSendDelivered(j.send_id):false;
      if(st) st.textContent=delivered?'✓ Primaries sent':'Sent (unconfirmed)';
      await loadDevicesInner();
      loadIJFrames();
    }catch(e){
      alert('Send failed: '+e.message);
      if(st) st.textContent='';
    }finally{
      if(btn) btn.disabled=!ijCurrentId;
    }
    return;
  }
  if(calibrationKind==='inkjoy-lo-ladder'){
    if(!ijCurrentId){alert('Select an InkJoy frame first.');return;}
    if(btn) btn.disabled=true;
    try{
      if(st) st.textContent='Push 1/2: black prime…';
      const j1=await readJSONResponse(await fetch('/api/calibration/inkjoy-black-248/send',{
        method:'POST',
        headers:sessionHeaders({'Content-Type':'application/json'}),
        body:JSON.stringify({device_id:ijCurrentId})
      }));
      if(st) st.textContent='Push 1/2: waiting for download…';
      const primeDelivered=j1.send_id?await waitSendDelivered(j1.send_id):false;
      if(st) st.textContent='Push 1/2: settle (90s)…';
      await new Promise(res=>setTimeout(res,90000));
      if(st) st.textContent='Push 2/2: lo ladder…';
      const j2=await readJSONResponse(await fetch('/api/calibration/inkjoy-lo-ladder/send',{
        method:'POST',
        headers:sessionHeaders({'Content-Type':'application/json'}),
        body:JSON.stringify({device_id:ijCurrentId})
      }));
      if(st) st.textContent='Push 2/2: waiting for download…';
      const delivered=j2.send_id?await waitSendDelivered(j2.send_id):false;
      if(st) st.textContent=delivered?'✓ Both plays sent':(primeDelivered?'Play 2 unconfirmed':'Sent (unconfirmed)');
      await loadDevicesInner();
      loadIJFrames();
    }catch(e){
      alert('Send failed: '+e.message);
      if(st) st.textContent='';
    }finally{
      if(btn) btn.disabled=!ijCurrentId;
    }
    return;
  }
  if(!samsungCurrentId){alert('Select a Samsung frame first.');return;}
  if(st) st.textContent='Pushing…';
  if(btn) btn.disabled=true;
  try{
    const r=await fetch('/api/samsung/'+encodeURIComponent(samsungCurrentId)+'/calibration',{method:'POST',headers:sessionHeaders()});
    if(!r.ok) throw new Error(await r.text());
    const j=await r.json();
    let delivered=false;
    try{ delivered=j.send_id?await watchSendProgress(j.send_id,{type:'samsung'}):false; }catch(_){}
    if(st) st.textContent=delivered?'✓ Sent to display':'Sent (unconfirmed)';
    await loadDevicesInner();
    loadSamsungFrames();
    await reloadSamsungFrame();
  }catch(e){
    alert('Send failed: '+e.message);
    if(st) st.textContent='';
  }finally{
    if(btn) btn.disabled=!samsungCurrentId;
  }
}

