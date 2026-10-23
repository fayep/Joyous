// ── Crop editor ──────────────────────────────────────────────────────────────
const FORMATS = {
  '4:3':  {ar:4/3},
  '3:4':  {ar:3/4},
  '16:9': {ar:16/9},
  '9:16': {ar:9/16},
  '1:1':  {ar:1},
  '√2:1': {ar:Math.SQRT2},   // landscape A-paper (e.g. A4 on its side)
  '1:√2': {ar:1/Math.SQRT2}, // portrait A-paper — matches the 28.5" panel (2160×3060, 12:17 ≈ 1:√2)
};

let cropId=null, cropAR=4/3, cropFmt='4:3';
let cropMeta=null;
let cropRect={x:0,y:0,w:1,h:1};    // normalised 0-1 (relative to source image)
let cropImgAR=1;                    // source image aspect ratio (w/h), set onload
let imgDisp={x:0,y:0,w:0,h:0};     // image rect in stage pixel coords
let allCrops={};
let drag=null;  // {type:'move'|handle, sx,sy, cr0, corner?}

const cropModal  = ()=>document.getElementById('crop-modal');
const cropImgEl  = ()=>document.getElementById('crop-img');
const cropBoxEl  = ()=>document.getElementById('crop-box');
const cropStageEl= ()=>document.getElementById('crop-stage');

// defaultCrop returns a centered crop rect in normalised source-image coordinates
// that achieves targetAR visually, given the source image has aspect ratio imgAR.
function defaultCrop(targetAR, imgAR){
  if(targetAR > imgAR){
    // target is wider than source: use full width, letterbox height
    const h = imgAR / targetAR;
    return {x:0, y:(1-h)/2, w:1, h};
  } else {
    // target is taller (or equal): use full height, pillarbox width
    const w = targetAR / imgAR;
    return {x:(1-w)/2, y:0, w, h:1};
  }
}

function updateDeleteBtn(){
  document.getElementById('crop-delete-btn').style.display = allCrops[cropFmt] ? '' : 'none';
}

function chromaModeFromMeta(meta){
  if(!meta||meta.chroma_boost==null) return 'global';
  return meta.chroma_boost?'on':'off';
}

function updateCropChromaUI(meta){
  const wrap=document.getElementById('crop-chroma-wrap');
  const hint=document.getElementById('crop-chroma-hint');
  const sel=document.getElementById('crop-chroma');
  if(!wrap||!hint||!sel) return;
  if(!meta||meta.flat_rgb){
    wrap.style.display='none';
    hint.textContent='';
    return;
  }
  wrap.style.display='inline-flex';
  sel.value=chromaModeFromMeta(meta);
  if(meta.people_likely){
    hint.textContent='People detected — warm shadow skin processing applies. Leave chroma off. (Unrelated to frame portrait orientation.)';
    hint.style.color='#c9a86c';
  }else{
    hint.textContent='No people detected — chroma on is a good fit for landscapes and still life';
    hint.style.color='#8fbf8f';
  }
}

async function saveChromaBoost(){
  if(!cropId) return;
  const mode=document.getElementById('crop-chroma').value;
  const r=await fetch('/api/images/'+cropId,{
    method:'PATCH',
    headers:{'Content-Type':'application/json'},
    body:JSON.stringify({chroma_boost_mode:mode})
  });
  if(!r.ok){ alert('Chroma save failed: '+(await r.text())); return; }
  cropMeta=await r.json();
  const img=images.find(i=>i.id===cropId);
  if(img){
    img.chroma_boost=cropMeta.chroma_boost;
    img.people_likely=cropMeta.people_likely;
  }
  updateCropChromaUI(cropMeta);
}

function openCrop(id){
  cropId=id; cropMeta=null; allCrops={...(imageCropsCache[id]||{})};
  cropFmt = Object.keys(FORMATS).find(k=>allCrops[k]) || '4:3';
  cropAR  = FORMATS[cropFmt].ar;
  document.getElementById('crop-format').value = cropFmt;
  imgDisp  = {x:0,y:0,w:0,h:0};
  updateCropChromaUI(null);

  fetch('/api/images/'+id).then(r=>r.ok?r.json():null).then(meta=>{
    if(!meta||cropId!==id) return;
    cropMeta=meta;
    const img=images.find(i=>i.id===id);
    if(img){
      img.chroma_boost=meta.chroma_boost;
      img.people_likely=meta.people_likely;
    }
    updateCropChromaUI(meta);
  }).catch(()=>{});

  const img = cropImgEl();
  img.onload = ()=>{
    cropImgAR = img.naturalWidth / img.naturalHeight;
    layoutImg();
    cropRect = allCrops[cropFmt] ? {...allCrops[cropFmt]} : defaultCrop(cropAR, cropImgAR);
    renderBox();
    renderOverlays();
    updateDeleteBtn();
  };
  img.src = '/images/'+id+'/preview';
  cropModal().classList.add('open');
  setCropScrollLock(true);
}

function closeCrop(){
  cropModal().classList.remove('open');
  cropId=null;
  cropMeta=null;
  endCropDrag();
  setCropScrollLock(false);
}

let cropScrollLockY=0;
function setCropScrollLock(on){
  const html=document.documentElement, body=document.body;
  if(on){
    cropScrollLockY=window.scrollY;
    body.style.position='fixed';
    body.style.top='-'+cropScrollLockY+'px';
    body.style.left='0';
    body.style.right='0';
    body.style.width='100%';
    html.style.overflow='hidden';
    body.style.overflow='hidden';
  }else{
    body.style.position='';
    body.style.top='';
    body.style.left='';
    body.style.right='';
    body.style.width='';
    html.style.overflow='';
    body.style.overflow='';
    window.scrollTo(0,cropScrollLockY);
  }
}

function onFormatChange(){
  cropFmt = document.getElementById('crop-format').value;
  cropAR  = FORMATS[cropFmt].ar;
  cropRect = allCrops[cropFmt] ? {...allCrops[cropFmt]} : defaultCrop(cropAR, cropImgAR);
  renderBox();
  renderOverlays();
  updateDeleteBtn();
}

async function saveCrop(){
  if(!cropId) return;
  const r = await fetch('/api/images/'+cropId+'/crop',{
    method:'POST', headers:{'Content-Type':'application/json'},
    body:JSON.stringify({format:cropFmt, ...cropRect})
  });
  if(!r.ok){ alert('Save failed: '+(await r.text())); return; }
  allCrops[cropFmt]={...cropRect};
  if(!imageCropsCache[cropId]) imageCropsCache[cropId]={};
  imageCropsCache[cropId][cropFmt]={...cropRect};
  renderOverlays();
  updateDeleteBtn();
  refreshThumb(cropId);
}

function downloadCropImage(){
  if(!cropId) return;
  const img=images.find(i=>i.id===cropId)||cropMeta;
  downloadAlbumImage(cropId, img&&(img.name||img.Name)||cropId);
}

async function deleteCrop(){
  if(!cropId || !allCrops[cropFmt]) return;
  const r = await fetch('/api/images/'+cropId+'/crop?format='+encodeURIComponent(cropFmt),{method:'DELETE'});
  if(!r.ok){ alert('Delete failed: '+(await r.text())); return; }
  delete allCrops[cropFmt];
  if(imageCropsCache[cropId]) delete imageCropsCache[cropId][cropFmt];
  cropRect = defaultCrop(cropAR, cropImgAR);
  renderBox();
  renderOverlays();
  updateDeleteBtn();
  refreshThumb(cropId);
}

function refreshThumb(id){
  const card=document.getElementById('card-'+id);
  if(!card) return;
  const t=card.querySelector('img');
  if(!t) return;
  const url='/images/'+encodeURIComponent(id)+'/preview';
  t.src='';
  t.src=url;
}

function refreshSamsungPreview(url){
  const t=document.getElementById('samsung-preview');
  if(!t||!samsungCurrentId) return;
  if(!url) url=samsungPreviewURL(samsungCurrentId, samsungPreviewKey);
  t.src='';
  t.src=url;
}

function refreshIJPreview(url){
  const t=document.getElementById('ij-preview');
  if(!t) return;
  t.src='';
  t.src=url;
}

// ── layout ───────────────────────────────────────────────────────────────────
function layoutImg(){
  const stage = cropStageEl(), img = cropImgEl();
  const sw = stage.clientWidth, sh = stage.clientHeight;
  const iw = img.naturalWidth,  ih = img.naturalHeight;
  const scale = Math.min(sw/iw, sh/ih);
  const dw = iw*scale, dh = ih*scale;
  const ox = (sw-dw)/2, oy = (sh-dh)/2;
  img.style.cssText = 'left:'+ox+'px;top:'+oy+'px;width:'+dw+'px;height:'+dh+'px';
  imgDisp = {x:ox,y:oy,w:dw,h:dh};
}

function renderBox(){
  if(!imgDisp.w) return;
  const {x,y,w,h} = cropRect;
  const bx = imgDisp.x + x*imgDisp.w;
  const by = imgDisp.y + y*imgDisp.h;
  const bw = w*imgDisp.w, bh = h*imgDisp.h;
  cropBoxEl().style.cssText = 'left:'+bx+'px;top:'+by+'px;width:'+bw+'px;height:'+bh+'px';
  document.getElementById('crop-hint').textContent =
    Math.round(x*100)+'%, '+Math.round(y*100)+'%  —  '+Math.round(w*100)+'% × '+Math.round(h*100)+'%';
}

// renderOverlays redraws dim dashed boxes for all saved crops except the active one.
function renderOverlays(){
  if(!imgDisp.w) return;
  const stage = cropStageEl();
  stage.querySelectorAll('.crop-dim,.crop-dim-label').forEach(el=>el.remove());
  for(const [fmt, rect] of Object.entries(allCrops)){
    if(fmt===cropFmt) continue;
    const bx = imgDisp.x + rect.x*imgDisp.w;
    const by = imgDisp.y + rect.y*imgDisp.h;
    const bw = rect.w*imgDisp.w, bh = rect.h*imgDisp.h;
    const div = document.createElement('div');
    div.className = 'crop-dim';
    div.style.cssText = 'left:'+bx+'px;top:'+by+'px;width:'+bw+'px;height:'+bh+'px';
    const lbl = document.createElement('span');
    lbl.className = 'crop-dim-label';
    lbl.textContent = fmt;
    div.appendChild(lbl);
    stage.appendChild(div);
  }
}

// ── drag ─────────────────────────────────────────────────────────────────────
function clamp(v,lo,hi){ return Math.max(lo,Math.min(hi,v)); }

function applyDrag(e){
  if(!drag||!imgDisp.w) return;
  const dx=(e.clientX-drag.sx)/imgDisp.w;
  const dy=(e.clientY-drag.sy)/imgDisp.h;
  let {x,y,w,h}=drag.cr0;

  if(drag.type==='move'){
    x=clamp(x+dx, 0, 1-w);
    y=clamp(y+dy, 0, 1-h);
    cropRect={x,y,w,h};
  } else {
    // corner resize — anchor is opposite corner
    const c=drag.corner;
    const ax = c.includes('r') ? x     : x+w;  // anchor x
    const ay = c.includes('b') ? y     : y+h;  // anchor y
    let fx  = c.includes('r') ? x+w+dx : x+dx;
    let fy  = c.includes('b') ? y+h+dy : y+dy;
    fx=clamp(fx,0,1); fy=clamp(fy,0,1);

    let nw=Math.abs(fx-ax), nh=Math.abs(fy-ay);
    // normAR: the w/h ratio in normalised coords that produces visual cropAR.
    // (nw * imgDisp.w) / (nh * imgDisp.h) = cropAR  =>  nw/nh = cropAR / cropImgAR
    const normAR = cropAR / cropImgAR;
    if(nw/normAR > nh){ nh=nw/normAR; } else { nw=nh*normAR; }
    // clamp to image bounds from anchor, then re-enforce ratio
    nw=Math.min(nw, c.includes('r') ? 1-ax : ax);
    nh=Math.min(nh, c.includes('b') ? 1-ay : ay);
    if(nw/normAR > nh){ nw=nh*normAR; } else { nh=nw/normAR; }

    const nx = c.includes('r') ? ax     : ax-nw;
    const ny = c.includes('b') ? ay     : ay-nh;
    cropRect={x:nx,y:ny,w:nw,h:nh};
  }
  renderBox();
}

function cropHandleAt(x,y){
  for(const h of cropBoxEl().querySelectorAll('.ch')){
    const r=h.getBoundingClientRect();
    if(x>=r.left&&x<=r.right&&y>=r.top&&y<=r.bottom) return h.dataset.h;
  }
  return null;
}

function pointInCropBox(x,y){
  const r=cropBoxEl().getBoundingClientRect();
  return x>=r.left&&x<=r.right&&y>=r.top&&y<=r.bottom;
}

function beginCropDrag(clientX,clientY){
  if(!cropModal().classList.contains('open')||!imgDisp.w) return false;
  const corner=cropHandleAt(clientX,clientY);
  if(corner){
    drag={type:'corner',corner,sx:clientX,sy:clientY,cr0:{...cropRect}};
  }else if(pointInCropBox(clientX,clientY)){
    drag={type:'move',sx:clientX,sy:clientY,cr0:{...cropRect}};
  }else{
    return false;
  }
  return true;
}

function onCropDragMove(e){
  if(!drag) return;
  applyDrag(e);
  e.preventDefault();
}

function onCropTouchMove(e){
  if(!drag||!e.touches.length) return;
  applyDrag({clientX:e.touches[0].clientX,clientY:e.touches[0].clientY});
  e.preventDefault();
}

function endCropDrag(){
  drag=null;
  document.removeEventListener('pointermove',onCropDragMove);
  document.removeEventListener('pointerup',endCropDrag);
  document.removeEventListener('pointercancel',endCropDrag);
  document.removeEventListener('touchmove',onCropTouchMove);
  document.removeEventListener('touchend',endCropDrag);
  document.removeEventListener('touchcancel',endCropDrag);
}

function onStagePointerDown(e){
  if(e.pointerType==='touch') return; // touchstart handles touch; avoids double drag
  if(!beginCropDrag(e.clientX,e.clientY)) return;
  e.preventDefault();
  document.addEventListener('pointermove',onCropDragMove,{passive:false});
  document.addEventListener('pointerup',endCropDrag);
  document.addEventListener('pointercancel',endCropDrag);
}

function onStageTouchStart(e){
  if(e.touches.length!==1) return;
  const t=e.touches[0];
  if(!beginCropDrag(t.clientX,t.clientY)) return;
  e.preventDefault();
  document.addEventListener('touchmove',onCropTouchMove,{passive:false});
  document.addEventListener('touchend',endCropDrag);
  document.addEventListener('touchcancel',endCropDrag);
}

(function initCropDrag(){
  const stage=cropStageEl();
  const modal=cropModal();
  stage.addEventListener('pointerdown',onStagePointerDown);
  stage.addEventListener('touchstart',onStageTouchStart,{passive:false});
  // Stop iOS from scrolling the album grid behind the modal.
  modal.addEventListener('touchmove',e=>{
    if(modal.classList.contains('open')) e.preventDefault();
  },{passive:false});
})();
window.addEventListener('resize',()=>{ if(imgDisp.w){ layoutImg(); renderBox(); renderOverlays(); } });
