// ── Color calibration (Devices tab, InkJoy only) ─────────────────────────────
const colorCalState={};

function toggleColorCal(deviceId){
  const el=document.getElementById('colorcal-'+deviceId);
  if(!el)return;
  if(el.style.display==='none'){
    el.style.display='block';
    loadColorCal(deviceId);
  }else{
    el.style.display='none';
  }
}

async function loadColorCal(deviceId){
  const el=document.getElementById('colorcal-'+deviceId);
  if(!el)return;
  el.innerHTML='Loading…';
  try{
    const r=await fetch('/api/devices/'+encodeURIComponent(deviceId)+'/color');
    if(!r.ok) throw new Error(await r.text());
    const data=await r.json();
    colorCalState[deviceId]={
      names:data.names||['black','white','yellow','red','blue','green'],
      bridgeDefault:data.bridge_default||[],
      override:data.override||null
    };
    renderColorCal(deviceId);
  }catch(e){
    el.innerHTML='<p style="color:#c00;font-size:.85rem">Load failed: '+e.message+'</p>';
  }
}

function colorCalRowsHTML(deviceId, palette){
  const st=colorCalState[deviceId];
  const names=st&&st.names?st.names:['black','white','yellow','red','blue','green'];
  return '<table style="width:100%;font-size:.8rem;border-collapse:collapse;margin:.35rem 0 .5rem"><thead><tr><th style="text-align:left;padding:.2rem 0">Ink</th><th>R</th><th>G</th><th>B</th><th></th></tr></thead><tbody>'+
    names.map((n,i)=>{
      const rgb=(palette&&palette[i])||[0,0,0];
      const id='devclr-'+deviceId+'-'+i;
      return '<tr><td style="padding:.2rem .35rem .2rem 0">'+n+'</td>'+
        '<td><input id="'+id+'-r" type="number" min="0" max="255" value="'+rgb[0]+'" oninput="updateColorCalSwatch(\''+deviceId+'\','+i+')" style="width:3.2rem;padding:.2rem;border:1px solid #ccc;border-radius:3px"></td>'+
        '<td><input id="'+id+'-g" type="number" min="0" max="255" value="'+rgb[1]+'" oninput="updateColorCalSwatch(\''+deviceId+'\','+i+')" style="width:3.2rem;padding:.2rem;border:1px solid #ccc;border-radius:3px"></td>'+
        '<td><input id="'+id+'-b" type="number" min="0" max="255" value="'+rgb[2]+'" oninput="updateColorCalSwatch(\''+deviceId+'\','+i+')" style="width:3.2rem;padding:.2rem;border:1px solid #ccc;border-radius:3px"></td>'+
        '<td><span id="'+id+'-sw" style="display:inline-block;width:1.4rem;height:1.1rem;border:1px solid #ccc;border-radius:3px;vertical-align:middle;background:rgb('+rgb.join(',')+')"></span></td></tr>';
    }).join('')+'</tbody></table>';
}

function updateColorCalSwatch(deviceId, i){
  const id='devclr-'+deviceId+'-'+i;
  const r=parseInt(document.getElementById(id+'-r').value,10)||0;
  const g=parseInt(document.getElementById(id+'-g').value,10)||0;
  const b=parseInt(document.getElementById(id+'-b').value,10)||0;
  const sw=document.getElementById(id+'-sw');
  if(sw) sw.style.background='rgb('+Math.max(0,Math.min(255,r))+','+Math.max(0,Math.min(255,g))+','+Math.max(0,Math.min(255,b))+')';
}

function readColorCalPalette(deviceId){
  const st=colorCalState[deviceId];
  const n=(st&&st.names?st.names.length:6);
  const out=[];
  for(let i=0;i<n;i++){
    const id='devclr-'+deviceId+'-'+i;
    const r=parseInt(document.getElementById(id+'-r').value,10)||0;
    const g=parseInt(document.getElementById(id+'-g').value,10)||0;
    const b=parseInt(document.getElementById(id+'-b').value,10)||0;
    out.push([Math.max(0,Math.min(255,r)),Math.max(0,Math.min(255,g)),Math.max(0,Math.min(255,b))]);
  }
  return out;
}

function renderColorCal(deviceId){
  const el=document.getElementById('colorcal-'+deviceId);
  if(!el)return;
  const st=colorCalState[deviceId];
  const usingOverride=!!(st&&st.override);
  const palette=usingOverride?st.override:(st?st.bridgeDefault:[]);
  el.innerHTML=
    '<div style="color:#666;font-size:.85rem;margin-bottom:.4rem">'+
      (usingOverride?'Custom calibration for this panel.':'Using the standard palette for this panel.')+
    '</div>'+
    colorCalRowsHTML(deviceId, palette)+
    '<div style="display:flex;gap:.4rem;flex-wrap:wrap;margin-top:.4rem">'+
      '<button type="button" class="btn btn-sm" onclick="sendColorCalChart(\''+deviceId+'\')">Send calibration image</button>'+
      '<button type="button" class="btn btn-sm btn-primary" onclick="saveColorCal(\''+deviceId+'\')">Save</button>'+
      '<button type="button" class="btn btn-sm" onclick="resetColorCal(\''+deviceId+'\')"'+(usingOverride?'':' disabled')+'>Reset to standard</button>'+
    '</div>'+
    '<span id="colorcal-status-'+deviceId+'" style="display:block;margin-top:.4rem;color:#666;font-size:.85rem"></span>';
}

async function sendColorCalChart(deviceId){
  const st=document.getElementById('colorcal-status-'+deviceId);
  if(st) st.textContent='Sending calibration chart…';
  const dev=devices.find(d=>d.id===deviceId);
  const type=dev&&dev.type||'inkjoy';
  try{
    let delivered=false;
    if(type==='samsung'){
      const r=await fetch('/api/samsung/'+encodeURIComponent(deviceId)+'/calibration',{method:'POST',headers:sessionHeaders()});
      if(!r.ok) throw new Error(await r.text());
      const j=await r.json();
      delivered=j.send_id?await watchSendProgress(j.send_id,{type:'samsung'}):false;
    }else{
      const j=await readJSONResponse(await fetch('/api/calibration/inkjoy/send',{
        method:'POST',
        headers:sessionHeaders({'Content-Type':'application/json'}),
        body:JSON.stringify({device_id:deviceId})
      }));
      delivered=j.send_id?await waitSendDelivered(j.send_id):false;
    }
    if(st) st.textContent=delivered?'✓ Chart sent — compare it against the physical panel, then adjust and save.':'Sent (unconfirmed)';
  }catch(e){
    if(st) st.textContent='Send failed: '+e.message;
  }
}

async function saveColorCal(deviceId){
  const st=document.getElementById('colorcal-status-'+deviceId);
  if(st) st.textContent='Saving…';
  try{
    const palette=readColorCalPalette(deviceId);
    const r=await fetch('/api/devices/'+encodeURIComponent(deviceId)+'/color',{
      method:'PUT',
      headers:sessionHeaders({'Content-Type':'application/json'}),
      body:JSON.stringify({palette})
    });
    if(!r.ok) throw new Error(await r.text());
    if(colorCalState[deviceId]) colorCalState[deviceId].override=palette;
    renderColorCal(deviceId);
    const st2=document.getElementById('colorcal-status-'+deviceId);
    if(st2) st2.textContent='✓ Saved — next send to this panel uses these values.';
  }catch(e){
    if(st) st.textContent='Save failed: '+e.message;
  }
}

async function resetColorCal(deviceId){
  const st=document.getElementById('colorcal-status-'+deviceId);
  if(st) st.textContent='Resetting…';
  try{
    const r=await fetch('/api/devices/'+encodeURIComponent(deviceId)+'/color',{
      method:'PUT',
      headers:sessionHeaders({'Content-Type':'application/json'}),
      body:JSON.stringify({palette:null})
    });
    if(!r.ok) throw new Error(await r.text());
    await loadColorCal(deviceId);
    const st2=document.getElementById('colorcal-status-'+deviceId);
    if(st2) st2.textContent='✓ Reset to the standard palette.';
  }catch(e){
    if(st) st.textContent='Reset failed: '+e.message;
  }
}

showTab('album', document.querySelector('nav button.active'));
loadAlbums();
loadAllTags();

