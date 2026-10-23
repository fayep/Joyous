// ── BLE adopt ────────────────────────────────────────────────────────────────
let adoptTarget = null;

async function startBLEScan(){
  const btn=document.getElementById('ble-scan-btn');
  const st=document.getElementById('ble-scan-status');
  const res=document.getElementById('ble-scan-results');
  btn.disabled=true; st.textContent='Scanning for 8 seconds…'; res.innerHTML='';
  try{
    const r=await fetch('/inkjoy/api/ble/scan',{method:'POST'});
    if(!r.ok) throw new Error(await r.text());
    const frames=await r.json();
    if(!frames||!frames.length){st.textContent='No InkJoy frames found nearby.';return;}
    st.textContent=frames.length+' frame(s) found:';
    res.innerHTML=frames.map(f=>
      '<div class="frame-list-item" style="background:#f0f7ff;margin-bottom:.3rem" onclick="openAdopt('+JSON.stringify(f)+')" title="Click to adopt">'+
        '<div class="dot offline"></div>'+
        '<span style="font-weight:500;font-size:.9rem">'+f.name+'</span>'+
        '<button class="btn btn-sm btn-primary" style="margin-left:auto;font-size:.75rem" onclick="event.stopPropagation();openAdopt('+JSON.stringify(f)+')">Adopt</button>'+
      '</div>'
    ).join('');
  }catch(e){
    st.textContent='Scan failed: '+e.message;
  }finally{
    btn.disabled=false;
  }
}

function openAdopt(frame){
  adoptTarget=frame;
  document.getElementById('adopt-frame-name').textContent=frame.name+'  '+frame.mac;
  document.getElementById('adopt-ssid').value='';
  document.getElementById('adopt-pwd').value='';
  document.getElementById('adopt-status').textContent='';
  document.getElementById('adopt-submit-btn').disabled=false;
  document.getElementById('adopt-modal').style.display='flex';
  setTimeout(()=>document.getElementById('adopt-ssid').focus(),100);
}

function closeAdopt(){
  document.getElementById('adopt-modal').style.display='none';
  adoptTarget=null;
}

async function submitAdopt(){
  if(!adoptTarget)return;
  const ssid=document.getElementById('adopt-ssid').value.trim();
  const pwd=document.getElementById('adopt-pwd').value;
  if(!ssid){alert('Enter WiFi network name');return;}
  const st=document.getElementById('adopt-status');
  const btn=document.getElementById('adopt-submit-btn');
  st.textContent='Connecting via Bluetooth…'; btn.disabled=true;
  try{
    const r=await fetch('/inkjoy/api/ble/adopt',{
      method:'POST',headers:{'Content-Type':'application/json'},
      body:JSON.stringify({address:adoptTarget.address,ssid,wifi_pwd:pwd})
    });
    if(!r.ok) throw new Error(await r.text());
    st.style.color='#28a745'; st.textContent='Frame adopted! Waiting for it to connect…';
    setTimeout(async()=>{
      closeAdopt();
      await loadDevicesInner(); loadIJFrames();
      document.getElementById('ble-scan-results').innerHTML='';
      document.getElementById('ble-scan-status').textContent='';
    }, 4000);
  }catch(e){
    st.style.color='#dc3545'; st.textContent='Failed: '+e.message;
    btn.disabled=false;
  }
}

