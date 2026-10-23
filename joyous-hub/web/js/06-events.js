// connectJoyousEvents opens the single event stream (see event_bus.go) that replaces what used
// to be five independently-polled endpoints: devices, bridge nav status, active sends, UI
// revision, and the Album tab's image-catalog poll. EventSource reconnects automatically on
// any drop — each (re)connection gets a fresh handshake snapshot of everything (see
// handleEvents), so a missed event during a brief disconnect is corrected by the next snapshot
// rather than needing replay.
function connectJoyousEvents(){
  const es=new EventSource('/api/events?session='+encodeURIComponent(joyousSessionId));
  es.onmessage=(evt)=>{
    let msg;
    try{ msg=JSON.parse(evt.data); }catch(e){ return; }
    switch(msg.type){
      case 'devices': applyDevicesSnapshot(msg.data); break;
      case 'bridges': applyBridgeStatus(msg.data); break;
      case 'send': onSendDiscoveryEvent(msg.data); break;
      case 'error': applyErrorEvent(msg.data); break;
      case 'notify': applyNotifyEvent(msg.data); break;
      case 'revision': applyRevisionEvent(msg.data); break;
      case 'images': applyImagesEvent(msg.data); break;
    }
  };
}
connectJoyousEvents();

async function doSend(imageId, deviceId, feedbackBtn){
  closePickers();
  const orig=feedbackBtn?feedbackBtn.textContent:'';
  const dev=devices.find(d=>d.id===deviceId);
  const deepHint=samsungDeepSleepWakeHint(dev);
  if(feedbackBtn){
    feedbackBtn.disabled=true;
    feedbackBtn.textContent=deepHint||'Sending…';
  }else if(deepHint){
    alert(deepHint);
  }
  let sendId;
  try{
    sendId=await postDisplay(deviceId,imageId);
  }catch(e){
    alert('Send failed: '+e.message);
    if(feedbackBtn){feedbackBtn.textContent=orig;feedbackBtn.disabled=false;}
    return;
  }
  if(sendId&&feedbackBtn&&dev?.type!=='samsung') feedbackBtn.textContent='Downloading…';
  let delivered=false;
  try{
    delivered=sendId?await watchSendProgress(sendId,dev):false;
  }catch(e){
    // watchSendProgress already surfaced this via toast.
  }
  if(feedbackBtn){
    feedbackBtn.textContent=delivered?'✓ Sent':(sendId?'Sent (unconfirmed)':'✓ Sent');
    setTimeout(()=>{feedbackBtn.textContent=orig;feedbackBtn.disabled=false;},2000);
  }
}

function sendImageToFrame(evt, imageId){
  if(evt) evt.stopPropagation();
  const btn=document.getElementById('send-btn-'+imageId);
  ensureDevicesForSend().then(()=>{
    const frameDevices=devices.filter(d=>d.type==='inkjoy'||d.type==='samsung'||d.type==='nixplay');
    if(!frameDevices.length){alert('No frames registered — check Devices tab or Discover.');return;}
    if(frameDevices.length===1){doSend(imageId,frameDevices[0].id,btn);return;}
    if(sendPickerImageId===imageId){closePickers();return;}
    sendPickerImageId=imageId;
    sendPicker.innerHTML=frameDevices.map(d=>{
      const label=d.name||(d.mac?d.mac:d.ip)||d.id;
      return '<div class="send-picker-item" onclick="doSend(\''+imageId+'\',\''+d.id+'\',document.getElementById(\'send-btn-'+imageId+'\'))">'+label+'</div>';
    }).join('');
    positionSendPicker(btn.getBoundingClientRect(),{preferAbove:!!btn.closest('.album-menu')});
  });
}

async function sendToFrame(deviceId){
  const frameImages=images;
  if(!frameImages.length){alert('No images in album — upload one first.');return;}
  if(frameImages.length===1){doSend(frameImages[0].id,deviceId,null);return;}
  // Show image picker modal — reuse album grid logic with a simple prompt for now
  // (this path is rarely used; main flow is Album → Send)
  const imageId=prompt('Image ID to send:\n'+frameImages.map(i=>i.id.slice(0,8)+' '+i.name).join('\n'));
  if(!imageId)return;
  const match=frameImages.find(i=>i.id.startsWith(imageId.trim()))||frameImages.find(i=>i.name.includes(imageId.trim()));
  if(!match){alert('Image not found');return;}
  doSend(match.id,deviceId,null);
}

async function deleteImg(id){
  if(!confirm('Delete image?'))return;
  if(albumZoomId===id) closeAlbumZoom();
  await fetch('/api/images/'+id,{method:'DELETE'});
  if(albumSelectedId===id) albumSelectedId=null;
  loadImages();
}

document.getElementById('file-input').addEventListener('change',async e=>{
  for(const f of e.target.files){
    const fd=new FormData(); fd.append('file',f);
    await fetch('/api/images',{method:'POST',body:fd});
  }
  loadImages();
});

const zone=document.getElementById('upload-zone');
zone.addEventListener('dragover',e=>{e.preventDefault();zone.classList.add('drag')});
zone.addEventListener('dragleave',()=>zone.classList.remove('drag'));
zone.addEventListener('drop',async e=>{
  e.preventDefault();zone.classList.remove('drag');
  for(const f of e.dataTransfer.files){
    const fd=new FormData();fd.append('file',f);
    await fetch('/api/images',{method:'POST',body:fd});
  }
  loadImages();
});

