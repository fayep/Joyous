let mqttLogTimer=null;
const mqttExpanded=new Set();
let mqttHideUIInitialized=false;
function mqttEntryKey(e){ return e.time+'\0'+e.topic+'\0'+e.dir; }
function initMqttHideUI(){
  if(mqttHideUIInitialized) return;
  mqttHideUIInitialized=true;
  const cb=document.getElementById('mqtt-hide-ui');
  if(!cb) return;
  cb.checked=localStorage.getItem('mqtt-hide-ui')==='1';
}
function onMqttHideUIChange(){
  const cb=document.getElementById('mqtt-hide-ui');
  if(cb) localStorage.setItem('mqtt-hide-ui',cb.checked?'1':'0');
  for(const id of ['mqtt-local','mqtt-upstream']){
    const el=document.getElementById(id);
    if(el) delete el.dataset.mqttSig;
  }
  loadMQTTLogs();
}
function mqttEntryImportant(e){
  const a=e.action||'';
  if(a==='play'||a==='play_ack'||a.startsWith('bridge.cmd')) return true;
  if(a==='bridge.event'||a==='send.complete'||a.startsWith('send.complete')) return true;
  const body=e.body||'';
  if(body.indexOf('"cmd":"send.image"')>=0||body.indexOf('"action":"play"')>=0) return true;
  return false;
}
function mqttEntryHidden(e){
  if(mqttEntryImportant(e)) return false;
  if(!e.action) return false;
  if(e.action.startsWith('ui.')) return true;
  return e.action==='devices.sync'||e.action==='bridge.hello'||e.action==='ui.state';
}
function filterMQTTEntries(entries){
  const cb=document.getElementById('mqtt-hide-ui');
  if(!cb||!cb.checked) return entries;
  return (entries||[]).filter(e=>!mqttEntryHidden(e));
}
function startMQTTLogPoll(){
  if(mqttLogTimer) return;
  initMqttHideUI();
  loadMQTTLogs();
  mqttLogTimer=setInterval(loadMQTTLogs, 1000);
}
function stopMQTTLogPoll(){
  if(mqttLogTimer){ clearInterval(mqttLogTimer); mqttLogTimer=null; }
}
function esc(s){
  return String(s).replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;');
}
function mqttBodyLong(body){
  return body && (body.length>180 || body.split('\n').length>6);
}
function mqttSelectionIn(el){
  const sel=window.getSelection();
  if(!sel||sel.isCollapsed||!sel.rangeCount) return false;
  let node=sel.anchorNode;
  if(!node) return false;
  if(node.nodeType===Node.TEXT_NODE) node=node.parentElement;
  return node&&el.contains(node);
}
function mqttEntryHTML(e){
  const note=e.note?'<span class="note">'+esc(e.note)+'</span>':'';
  const action=e.action?'<span class="action">'+esc(e.action)+'</span>':'';
  const longBody=mqttBodyLong(e.body);
  const key=mqttEntryKey(e);
  const expanded=mqttExpanded.has(key);
  const cls='mqtt-entry'+(longBody?' clampable':'')+(expanded?' expanded':'');
  const body=e.body?'<pre>'+esc(e.body)+'</pre>':'';
  const copyBtn=e.body?'<button type="button" class="mqtt-copy" onclick="event.stopPropagation();copyMQTTPayload(this)" title="Copy payload">Copy</button>':'';
  const hint=longBody&&!expanded?'<div class="mqtt-expand-hint">Click row to expand</div>':'';
  const expandAttr=longBody?' onclick="toggleMQTTEntry(this,event)"':'';
  const keyAttr=' data-mqtt-key="'+encodeURIComponent(key)+'"';
  return '<div class="'+cls+'"'+keyAttr+expandAttr+'><div class="meta"><span class="time">'+esc(e.time)+'</span><span class="dir">'+esc(e.dir)+'</span>'+action+note+copyBtn+'</div><div class="topic">'+esc(e.topic)+'</div>'+body+hint+'</div>';
}
function toggleMQTTEntry(el,ev){
  if(ev&&(ev.target.closest('pre')||ev.target.closest('.mqtt-copy'))) return;
  const key=decodeURIComponent(el.dataset.mqttKey);
  if(mqttExpanded.has(key)) mqttExpanded.delete(key);
  else mqttExpanded.add(key);
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
  const tab=document.getElementById('tab-mqtt');
  if(!tab||tab.style.display==='none') return;
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
    const r=await fetch('/api/mqtt/logs');
    if(!r.ok) return;
    const data=await r.json();
    updateMQTTColumn('mqtt-local',filterMQTTEntries(data.local));
    updateMQTTColumn('mqtt-upstream',filterMQTTEntries(data.upstream));
  }catch(_){}
}

let samsungLogTimer=null;
function startSamsungLogPoll(){
  if(samsungLogTimer) return;
  loadSamsungLogs();
  samsungLogTimer=setInterval(loadSamsungLogs, 1000);
}
function stopSamsungLogPoll(){
  if(samsungLogTimer){ clearInterval(samsungLogTimer); samsungLogTimer=null; }
}
function samsungLogEntryHTML(e){
  const peer=e.peer?'<span class="peer">'+esc(e.peer)+'</span>':'';
  return '<div class="samsung-log-entry"><span class="meta">'+esc(e.time||'')+'</span><span class="phase">'+esc(e.phase||'')+'</span>'+peer+esc(e.text||'')+'</div>';
}
async function loadSamsungLogs(){
  try{
    const r=await fetch('/api/samsung/logs');
    if(!r.ok) return;
    const data=await r.json();
    const el=document.getElementById('samsung-handshake-log');
    if(!el) return;
    // Oldest → newest (top → bottom), matching how the session actually ran.
    const list=data.entries||[];
    if(!list.length){
      el.innerHTML='<p class="mqtt-empty">No messages yet.</p>';
      return;
    }
    const sig=list.map(e=>(e.time||'')+'\0'+(e.text||'')).join('|');
    if(el.dataset.samsungSig===sig) return;
    const stickBottom=el.scrollHeight-el.scrollTop-el.clientHeight<40;
    el.dataset.samsungSig=sig;
    el.innerHTML=list.map(samsungLogEntryHTML).join('');
    if(stickBottom) el.scrollTop=el.scrollHeight;
  }catch(_){}
}

