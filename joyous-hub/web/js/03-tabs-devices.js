let tabRefreshTimer=null;
let tabRefreshFn=null;
let tabRefreshIntervalMs=0;
function startTabRefresh(intervalMs, fn){
  tabRefreshFn=fn;
  tabRefreshIntervalMs=intervalMs;
  resumeTabRefresh();
}
function resumeTabRefresh(){
  if(tabRefreshTimer){ clearInterval(tabRefreshTimer); tabRefreshTimer=null; }
  if(!tabRefreshFn||document.hidden) return;
  tabRefreshFn();
  tabRefreshTimer=setInterval(tabRefreshFn, tabRefreshIntervalMs);
}
function stopTabRefresh(){
  if(tabRefreshTimer){ clearInterval(tabRefreshTimer); tabRefreshTimer=null; }
  tabRefreshFn=null;
  tabRefreshIntervalMs=0;
}
document.addEventListener('visibilitychange',()=>{
  if(document.hidden){
    if(tabRefreshTimer){ clearInterval(tabRefreshTimer); tabRefreshTimer=null; }
  }else{
    resumeTabRefresh();
  }
});

async function refreshSamsungTab(){
  await loadSamsungFrames();
}


async function ensureDevicesForSend(){
  await loadDevicesInner();
}

async function loadDevices(){
  await loadDevicesInner();
  if(activeTab==='samsung') await loadSamsungFrames();
}

async function discoverFrames(){
  const btn=document.getElementById('discover-btn');
  const st=document.getElementById('discover-status');
  btn.disabled=true; st.textContent='Scanning network…';
  try{
    const r=await fetch('/api/devices/discover',{method:'POST'});
    const data=await r.json();
    if(!r.ok) throw new Error(data.error||r.statusText);
    const seen=data.ssdp_seen!=null?' ('+data.ssdp_seen+' UPnP devices)':'';
    st.textContent=data.found?'Found '+data.found+' frame(s)'+seen:'No frames matched'+(seen||'');
    loadDevices();
  }catch(e){
    st.textContent='Discovery failed: '+e.message;
  }finally{
    btn.disabled=false;
  }
}

async function refreshDevice(id){
  await fetch('/api/devices/'+encodeURIComponent(id)+'/refresh',{method:'POST'});
}

