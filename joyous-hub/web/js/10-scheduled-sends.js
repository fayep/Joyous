// ── Scheduled sends (Devices tab) ────────────────────────────────────────────

async function toggleSchedule(deviceId){
  const el=document.getElementById('sched-'+deviceId);
  if(!el)return;
  if(el.style.display==='none'){
    el.style.display='block';
    await loadSchedule(deviceId);
  }else{
    el.style.display='none';
  }
}

async function loadSchedule(deviceId){
  const el=document.getElementById('sched-'+deviceId);
  if(!el)return;
  el.innerHTML='Loading…';
  if(!albums||!albums.length) await loadAlbums();
  try{
    const r=await fetch('/api/devices/'+encodeURIComponent(deviceId)+'/schedule');
    const cfg=r.ok?await r.json():{};
    schedState[deviceId]={albumId:cfg.album_id||'',times:(cfg.times||[]).slice(),enabled:!!cfg.enabled};
  }catch(e){
    schedState[deviceId]={albumId:'',times:[],enabled:false};
  }
  renderSchedule(deviceId);
}

function renderSchedule(deviceId){
  const el=document.getElementById('sched-'+deviceId);
  if(!el)return;
  const st=schedState[deviceId]||{albumId:'',times:[],enabled:false};
  const albumOpts='<option value="">— choose album —</option>'+
    albums.map(a=>'<option value="'+a.id+'"'+(a.id===st.albumId?' selected':'')+'>'+escHtml(a.name||a.id)+'</option>').join('');
  // oninput (not only onchange): <input type="time"> often doesn't fire change until
  // blur, so Add/Save used to re-render from stale 09:00 defaults.
  const timeRows=st.times.map((t,i)=>
    '<div style="margin:.25rem 0"><input type="time" data-sched-time="1" value="'+escHtml(t)+'" oninput="updateSchedTime(\''+deviceId+'\','+i+',this.value)"> '+
    '<button type="button" class="btn btn-sm" onclick="removeSchedTime(\''+deviceId+'\','+i+')">✕</button></div>'
  ).join('');
  el.innerHTML=
    '<div style="color:#666;font-size:.85rem;margin-bottom:.4rem">Send a random photo from an album at each time below.</div>'+
    '<label style="display:block;margin-bottom:.4rem"><input type="checkbox" '+(st.enabled?'checked':'')+' onchange="setSchedEnabled(\''+deviceId+'\',this.checked)"> Enabled</label>'+
    '<select onchange="setSchedAlbum(\''+deviceId+'\',this.value)">'+albumOpts+'</select>'+
    '<div style="margin-top:.5rem">'+(timeRows||'<span style="color:#888;font-size:.85rem">No times set.</span>')+'</div>'+
    '<button type="button" class="btn btn-sm" onclick="addSchedTime(\''+deviceId+'\')">+ Add time</button> '+
    '<button type="button" class="btn btn-sm btn-primary" onclick="saveSchedule(\''+deviceId+'\')">Save</button>'+
    '<span id="sched-status-'+deviceId+'" style="margin-left:.5rem;color:#666;font-size:.85rem"></span>';
}

// Pull current <input type="time"> values into schedState before any re-render or save,
// so a just-picked time isn't lost if the browser hasn't fired a change event yet.
function syncSchedTimesFromDOM(deviceId){
  const st=schedState[deviceId];
  if(!st)return;
  const el=document.getElementById('sched-'+deviceId);
  if(!el)return;
  const inputs=el.querySelectorAll('input[data-sched-time]');
  if(!inputs.length)return;
  st.times=Array.from(inputs).map(inp=>inp.value||'').filter(v=>v);
}

function setSchedAlbum(deviceId,albumId){schedState[deviceId].albumId=albumId;}
function setSchedEnabled(deviceId,enabled){schedState[deviceId].enabled=enabled;}
function addSchedTime(deviceId){
  syncSchedTimesFromDOM(deviceId);
  const st=schedState[deviceId];
  if(!st)return;
  // Default new row to one hour after the last time (or 09:00 if empty).
  let next='09:00';
  if(st.times.length){
    const last=st.times[st.times.length-1];
    const m=last.match(/^(\d{1,2}):(\d{2})/);
    if(m){
      const mins=(parseInt(m[1],10)*60+parseInt(m[2],10)+60)%(24*60);
      next=String(Math.floor(mins/60)).padStart(2,'0')+':'+String(mins%60).padStart(2,'0');
    }
  }
  st.times.push(next);
  renderSchedule(deviceId);
}
function removeSchedTime(deviceId,i){
  syncSchedTimesFromDOM(deviceId);
  schedState[deviceId].times.splice(i,1);
  renderSchedule(deviceId);
}
function updateSchedTime(deviceId,i,v){
  if(!schedState[deviceId])return;
  schedState[deviceId].times[i]=v;
}

async function saveSchedule(deviceId){
  syncSchedTimesFromDOM(deviceId);
  const st=schedState[deviceId];
  if(!st)return;
  if(st.enabled&&!st.albumId){alert('Choose an album first.');return;}
  if(st.enabled&&!st.times.length){alert('Add at least one time first.');return;}
  const r=await fetch('/api/devices/'+encodeURIComponent(deviceId)+'/schedule',{
    method:'PUT',headers:{'Content-Type':'application/json'},
    body:JSON.stringify({album_id:st.albumId,times:st.times,enabled:st.enabled})
  });
  if(!r.ok){alert('Error: '+(await r.text()));return;}
  const status=document.getElementById('sched-status-'+deviceId);
  if(status){status.textContent='Saved';setTimeout(()=>{if(status)status.textContent='';},1500);}
}

