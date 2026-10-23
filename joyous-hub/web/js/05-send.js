const sendPicker=document.getElementById('send-picker');
let sendPickerImageId=null;
function closePickers(){
  sendPicker.style.display='none';
  sendPicker.style.maxHeight='';
  sendPicker.style.overflowY='';
  sendPickerImageId=null;
}
function positionSendPicker(anchor, opts){
  opts=opts||{};
  const margin=8;
  sendPicker.style.display='block';
  sendPicker.style.maxHeight='';
  sendPicker.style.overflowY='';
  sendPicker.style.left='0px';
  sendPicker.style.top='0px';
  const pickerW=sendPicker.offsetWidth;
  const pickerH=sendPicker.offsetHeight;
  const viewH=window.innerHeight;
  const viewW=window.innerWidth;
  let top;
  if(opts.preferAbove && anchor.top-4-pickerH>=margin){
    top=anchor.top-pickerH-4;
  }else{
    top=anchor.bottom+4;
    if(top+pickerH>viewH-margin && anchor.top-4-pickerH>=margin){
      top=anchor.top-pickerH-4;
    }
  }
  if(top+pickerH>viewH-margin){
    top=margin;
    sendPicker.style.maxHeight=(viewH-2*margin)+'px';
    sendPicker.style.overflowY='auto';
  }
  if(top<margin) top=margin;
  let left=anchor.left;
  if(left+pickerW>viewW-margin) left=viewW-pickerW-margin;
  if(left<margin) left=margin;
  sendPicker.style.top=top+'px';
  sendPicker.style.left=left+'px';
}
document.addEventListener('click',e=>{
  if(!sendPicker.contains(e.target)&&!e.target.id.startsWith('send-btn-')) closePickers();
});

function samsungDeepSleep(d,rec,s){
  return !!((s&&s.deep_sleep_active)||(rec&&rec.deep_sleep_active)||(d&&d.deep_sleep_active)||
    (d&&d.last_action==='mdc_deep_sleep')||(rec&&rec.last_action==='mdc_deep_sleep'));
}

const SAMSUNG_DEEP_SLEEP_WAKE_MSG='Press power button on frame for 3s to wake frame';

function samsungDeepSleepWakeHint(d,rec,s){
  return samsungDeepSleep(d,rec,s)?SAMSUNG_DEEP_SLEEP_WAKE_MSG:null;
}

async function postDisplay(deviceId,imageId){
  const r=await fetch('/api/devices/'+encodeURIComponent(deviceId)+'/display',{
    method:'POST',headers:sessionHeaders({'Content-Type':'application/json'}),
    body:JSON.stringify({image_id:imageId})
  });
  if(!r.ok) throw new Error(await r.text());
  const j=await r.json();
  return j.send_id||'';
}

const SEND_DELIVERY_TIMEOUT_MS=180000;

async function readJSONResponse(r){
  const text=await r.text();
  let j={};
  try{ j=JSON.parse(text); }catch(_){}
  if(!r.ok){
    throw new Error(j.error||text||r.statusText||'request failed');
  }
  return j;
}

async function waitSendDelivered(sendId,onTick){
  if(!sendId) return false;
  const deadline=Date.now()+SEND_DELIVERY_TIMEOUT_MS;
  while(Date.now()<deadline){
    const waitSec=Math.min(30, Math.max(1, Math.ceil((deadline-Date.now())/1000)));
    const ac=new AbortController();
    const kill=setTimeout(()=>ac.abort(), (waitSec+5)*1000);
    let r;
    try{
      r=await fetch('/api/send/'+encodeURIComponent(sendId)+'?wait='+waitSec,{signal:ac.signal});
    }catch(e){
      if(e&&e.name==='AbortError') continue;
      throw e;
    }finally{
      clearTimeout(kill);
    }
    if(r.status===404) return false;
    if(!r.ok) throw new Error(await r.text());
    const j=await r.json();
    if(j.status==='delivered') return true;
    if(j.status==='failed') throw new Error(j.error||'Frame did not finish downloading');
    if(onTick) onTick(j);
  }
  return false;
}

// showSendProgressToast renders live /api/send/{sendId} status as a top-right toast (not
// button/status-line text — a "Retrying (N)…" message crammed into a small button was hard to
// read and fighting for space with the button's own label). The "retrying" status and its detail
// come straight from the server — see send_delivery.go's IncrementRetry and samsung_mdc.go's
// waitMDCAwake/waitForMDCAwakeManual — rather than the client guessing from a fixed timer how
// long a send "should" take. The power-button hint only shows once detail says the automatic
// remote wake gave up (phase=manual server-side) — showing it during the automatic attempt would
// tell the user to do something the frame itself is still trying to do on its own.
function showSendProgressToast(toastId,j,dev){
  if(!toastId||!j) return;
  if(j.status==='retrying'){
    const hint=(dev&&dev.type==='samsung'&&j.detail==='waiting for frame to wake')?SAMSUNG_DEEP_SLEEP_WAKE_MSG+' — ':'';
    showToast(toastId,hint+'Retrying ('+j.retry_attempts+')…',{closable:true});
    return;
  }
  if(j.status==='downloading') showToast(toastId,'Downloading…',{closable:true});
}

// sendWatches dedupes concurrent watchers of the same sendId — e.g. the code that triggered a
// send and the automatic-send poller (see pollActiveSends) both discovering it within moments
// of each other. Later callers just await the same in-flight watch instead of polling
// /api/send/{sendId} twice for the same send.
const sendWatches={};
// sendWatchDone remembers sendIds this tab already finished watching, so an EventSource
// reconnect's active-send handshake doesn't resurrect a "Downloading…" toast for a zombie
// in-flight send the UI already timed out on.
const sendWatchDone={};

// watchSendProgress polls a send to completion, showing/updating a single toast the whole way
// (retrying/downloading -> final result) — the one place this happens, used by every send
// path (manual sends below, and pollActiveSends for sends nobody in this tab initiated).
function watchSendProgress(sendId,dev){
  if(!sendId) return Promise.resolve(false);
  if(sendWatchDone[sendId]) return Promise.resolve(false);
  if(sendWatches[sendId]) return sendWatches[sendId];
  const toastId='send-'+sendId;
  const p=(async()=>{
    try{
      const delivered=await waitSendDelivered(sendId,j=>{
        showSendProgressToast(toastId,j,dev);
      });
      showToast(toastId,delivered?'✓ Sent':'Sent (unconfirmed)',{type:'success',autoHideMs:2500,closable:true});
      return delivered;
    }catch(e){
      showToast(toastId,'Send failed: '+e.message,{type:'error',closable:true,autoHideMs:8000});
      throw e;
    }finally{
      delete sendWatches[sendId];
      sendWatchDone[sendId]=true;
    }
  })();
  sendWatches[sendId]=p;
  return p;
}

// onSendDiscoveryEvent handles a "send" event from the event stream for a sendId this tab
// isn't already watching — a scheduled send firing in the background is the main case. Starts
// watching it the same way as a manual send (progress toast, retrying/press-power-button hint
// included). A terminal event (delivered/failed) for a send we never saw the start of is
// skipped rather than popping a toast for something already finished — consistent with the
// event bus's "if you weren't listening you don't care" design (see event_bus.go).
function onSendDiscoveryEvent(data){
  if(!data||!data.send_id||sendWatches[data.send_id]||sendWatchDone[data.send_id]) return;
  if(data.status==='delivered'||data.status==='failed') return;
  watchSendProgress(data.send_id,{type:data.device_type});
}

function applyErrorEvent(data){
  if(!data) return;
  showToast('error-'+Date.now()+'-'+Math.random().toString(36).slice(2), data.message||'Something went wrong', {type:'error',closable:true,autoHideMs:10000});
}

// applyNotifyEvent shows a bridge→hub toast (protocol.EventNotify). Generic — any bridge can
// publish; Samsung uses it when remote wake fails and the user must press the power button.
function applyNotifyEvent(data){
  if(!data||!data.message) return;
  const id=data.id||('notify-'+Date.now()+'-'+Math.random().toString(36).slice(2));
  const closable=data.closable===undefined?true:!!data.closable;
  showToast(id, data.message, {
    type: data.level||'',
    closable: closable,
    autoHideMs: data.auto_hide_ms||0
  });
}

// applyImagesEvent reacts to a created/edited/deleted image or a reordered album (see
// imagesEventPayload in event_bus.go) by re-fetching the currently-viewed album — replacing
// the old fixed-interval Album tab poll and its /api/images/revision hash-compare entirely.
// A live re-fetch (via the same buildImagesURL()/loadImages() the tab already uses) is simpler
// and provably correct compared to re-implementing the server's tag/format/orientation smart-
// album filter matching in JS just to patch the DOM surgically, and on this app's LAN-local
// single-hub setup the extra round trip is cheap. Only fires while the Album tab is the one
// actually visible, and never while joyousAlbumGridBusy() (mid-edit — see its own doc comment;
// deliberately not joyousUIBusy(), which also counts the zoom modal as busy — see
// joyousAlbumGridBusy's doc comment for why that's wrong here).
function applyImagesEvent(data){
  if(!data||activeTab!=='album'||joyousAlbumGridBusy()) return;
  const removedVisible=data.removed&&data.removed.some(id=>images.some(img=>img.id===id));
  const updatedAny=data.updated&&data.updated.length>0;
  const reorderedCurrent=data.reordered_albums&&data.reordered_albums.includes(currentAlbumId);
  if(updatedAny||removedVisible||reorderedCurrent) loadImages();
}

