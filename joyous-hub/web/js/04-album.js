let imageCropsCache = {}; // id → crops map, kept fresh by loadImages + save/delete

function escHtml(s){
  return String(s).replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/"/g,'&quot;');
}

function downloadAlbumImage(id,name){
  const a=document.createElement('a');
  a.href='/images/'+encodeURIComponent(id)+'/original';
  a.download=name||id;
  a.rel='noopener';
  document.body.appendChild(a);
  a.click();
  a.remove();
}

function albumPhotoBaseHeightPx(){
  return window.matchMedia('(max-width:640px)').matches ? 120 : 148;
}

function albumPhotoMinDimPx(){
  return window.matchMedia('(max-width:640px)').matches ? 140 : 168;
}

function albumPhotoSize(img){
  const iw=img.width|0, ih=img.height|0;
  const baseH=albumPhotoBaseHeightPx();
  const minDim=albumPhotoMinDimPx();
  let photoW, photoH;
  if(iw>0&&ih>0){
    if(ih>iw){
      // Portrait: wide enough for buttons, height follows the photo.
      photoW=minDim;
      photoH=Math.round(photoW*ih/iw);
    }else{
      photoH=baseH;
      photoW=Math.round(photoH*iw/ih);
      const smallest=Math.min(photoW, photoH);
      if(smallest<minDim){
        const scale=minDim/smallest;
        photoW=Math.round(photoW*scale);
        photoH=Math.round(photoH*scale);
      }
    }
  }else{
    photoW=Math.round(baseH*4/3);
    photoH=baseH;
    const smallest=Math.min(photoW, photoH);
    if(smallest<minDim){
      const scale=minDim/smallest;
      photoW=Math.round(photoW*scale);
      photoH=Math.round(photoH*scale);
    }
  }
  return {w:photoW, h:photoH, portrait:iw>0&&ih>iw};
}

function albumStackVars(i){
  const rot=((i*41)%11)-5;
  const dy=(((i*29)%9)-4)*1.6;
  return '--rot:'+rot+'deg;--dy:'+dy+'px;--z:'+(i+1);
}

let albumSelectedId=null;
let albumCaptionEditingId=null;
let albumZoomId=null;
let albumZoomScrollY=0;

function layoutAlbumZoom(){
  const wrap=document.getElementById('album-zoom');
  const img=document.getElementById('album-zoom-img');
  if(!wrap||!wrap.classList.contains('open')||!img||!img.naturalWidth) return;
  const cap=document.getElementById('album-zoom-caption');
  const capH=cap&&cap.offsetHeight?cap.offsetHeight+16:0;
  const maxW=window.innerWidth-24;
  const maxH=window.innerHeight-24-capH;
  const scale=Math.min(maxW/img.naturalWidth, maxH/img.naturalHeight);
  img.style.width=Math.floor(img.naturalWidth*scale)+'px';
  img.style.height=Math.floor(img.naturalHeight*scale)+'px';
}

function openAlbumZoom(id){
  const meta=images.find(i=>i.id===id);
  if(!meta) return;
  const wrap=document.getElementById('album-zoom');
  const img=document.getElementById('album-zoom-img');
  const cap=document.getElementById('album-zoom-caption');
  if(!wrap||!img) return;
  albumZoomId=id;
  wrap.classList.add('open');
  wrap.setAttribute('aria-hidden','false');
  cap.textContent=meta.name||id;
  img.onload=()=>layoutAlbumZoom();
  img.src='/images/'+encodeURIComponent(id)+'/preview';
  if(img.complete&&img.naturalWidth) layoutAlbumZoom();
  albumZoomScrollY=window.scrollY;
  document.documentElement.style.overflow='hidden';
  document.body.style.overflow='hidden';
}

// rotateAlbumZoomImage sends an additional 90deg correction (see ImageMeta.RotateOverride in
// images.go) — for sources like legacy Nixplay imports whose rotation lived only in the
// exporting app's own database, never in EXIF, so decodeAnyImage's automatic correction alone
// can't fix them. Applies everywhere the image is rendered (thumbnail, preview, and every
// device's send path), not just this zoom view.
async function rotateAlbumZoomImage(delta){
  const id=albumZoomId;
  if(!id) return;
  const meta=images.find(i=>i.id===id);
  if(!meta) return;
  const buttons=[document.getElementById('album-zoom-rotate-ccw'),document.getElementById('album-zoom-rotate-cw')];
  buttons.forEach(b=>{ if(b) b.disabled=true; });
  try{
    const current=meta.rotate_override||0;
    const next=((current+delta)%360+360)%360;
    const r=await fetch('/api/images/'+encodeURIComponent(id),{
      method:'PATCH',headers:sessionHeaders({'Content-Type':'application/json'}),
      body:JSON.stringify({rotate_override:next})
    });
    if(!r.ok) throw new Error(await r.text());
    const updated=await r.json();
    Object.assign(meta,updated);
    const img=document.getElementById('album-zoom-img');
    if(img&&albumZoomId===id){
      img.onload=()=>layoutAlbumZoom();
      img.src='/images/'+encodeURIComponent(id)+'/preview?v='+next+'-'+Date.now();
    }
  }catch(e){
    showToast('rotate-'+id,'Rotate failed: '+e.message,{type:'error'});
  }finally{
    buttons.forEach(b=>{ if(b) b.disabled=false; });
  }
}

function closeAlbumZoom(){
  const wrap=document.getElementById('album-zoom');
  if(!wrap||!wrap.classList.contains('open')) return;
  wrap.classList.remove('open');
  wrap.setAttribute('aria-hidden','true');
  albumZoomId=null;
  const img=document.getElementById('album-zoom-img');
  if(img){
    img.onload=null;
    img.removeAttribute('src');
    img.style.width='';
    img.style.height='';
  }
  document.documentElement.style.overflow='';
  document.body.style.overflow='';
  window.scrollTo(0,albumZoomScrollY);
}

if(!window._albumZoomLayoutBound){
  window._albumZoomLayoutBound=true;
  window.addEventListener('resize',()=>layoutAlbumZoom());
  window.addEventListener('orientationchange',()=>setTimeout(layoutAlbumZoom,120));
  document.addEventListener('keydown',e=>{
    if(e.key==='Escape'&&albumZoomId) closeAlbumZoom();
  });
}

function albumCardInForeground(card){
  if(!card) return false;
  if(card.classList.contains('album-outer-selected')) return true;
  return window.matchMedia('(hover:hover)').matches&&card.matches(':hover');
}

function setAlbumSelected(id){
  const next=id||null;
  if(albumSelectedId===next) return;
  if(albumSelectedId){
    const prev=document.getElementById('card-'+albumSelectedId);
    if(prev) prev.classList.remove('album-outer-selected');
  }
  albumSelectedId=next;
  if(albumSelectedId){
    const el=document.getElementById('card-'+albumSelectedId);
    if(el) el.classList.add('album-outer-selected');
  }
}

function albumGridTap(e){
  if(e.target.closest('.album-btn')) return;
  const card=e.target.closest('.album-outer');
  if(!card) return;
  const imageId=card.id.slice(5);
  if(e.target.closest('.album-caption-wrap')){
    albumCaptionClick(e,imageId);
    return;
  }
  if(albumSelectedId===imageId&&e.target.closest('.album-img')){
    openAlbumZoom(imageId);
    return;
  }
  setAlbumSelected(imageId);
}

function albumCaptionClick(e,imageId){
  e.stopPropagation();
  if(albumCaptionEditingId) return;
  const card=document.getElementById('card-'+imageId);
  if(!albumCardInForeground(card)) return;
  startAlbumCaptionEdit(imageId);
}

function updateAlbumCaptionElement(imageId,name){
  const cap=document.querySelector('#card-'+imageId+' .album-caption');
  if(!cap) return;
  cap.textContent=name;
  cap.title=name;
}

function syncOverlayPreviewImageOption(imageId,name){
  const sel=document.getElementById('ovl-preview-image');
  if(!sel) return;
  const opt=sel.querySelector('option[value="'+imageId+'"]');
  if(opt) opt.textContent=name||imageId;
}

async function saveAlbumCaption(imageId,name){
  name=String(name||'').trim();
  const img=images.find(i=>i.id===imageId);
  if(!name){
    if(img) updateAlbumCaptionElement(imageId,img.name);
    return;
  }
  if(img&&img.name===name){
    updateAlbumCaptionElement(imageId,name);
    return;
  }
  try{
    const r=await fetch('/api/images/'+imageId,{
      method:'PATCH',
      headers:{'Content-Type':'application/json'},
      body:JSON.stringify({name})
    });
    if(!r.ok) throw new Error(await r.text());
    const meta=await r.json();
    if(img) img.name=meta.name;
    updateAlbumCaptionElement(imageId,meta.name);
    syncOverlayPreviewImageOption(imageId,meta.name);
  }catch(err){
    alert('Rename failed: '+err.message);
    if(img) updateAlbumCaptionElement(imageId,img.name);
  }
}

function startAlbumCaptionEdit(imageId){
  const wrap=document.querySelector('#card-'+imageId+' .album-caption-wrap');
  const cap=wrap&&wrap.querySelector('.album-caption');
  if(!wrap||!cap||albumCaptionEditingId) return;
  const img=images.find(i=>i.id===imageId);
  const input=document.createElement('input');
  input.type='text';
  input.className='album-caption-input';
  input.value=img?img.name:cap.textContent;
  input.setAttribute('aria-label','Photo name');
  cap.hidden=true;
  wrap.appendChild(input);
  albumCaptionEditingId=imageId;
  input.focus();
  input.select();
  function finish(save){
    input.removeEventListener('blur',onBlur);
    input.removeEventListener('keydown',onKey);
    cap.hidden=false;
    input.remove();
    albumCaptionEditingId=null;
    if(save) saveAlbumCaption(imageId,input.value);
    else if(img) updateAlbumCaptionElement(imageId,img.name);
  }
  function onBlur(){ finish(true); }
  function onKey(ev){
    if(ev.key==='Enter'){ ev.preventDefault(); finish(true); }
    if(ev.key==='Escape'){ ev.preventDefault(); finish(false); }
  }
  input.addEventListener('blur',onBlur);
  input.addEventListener('keydown',onKey);
}

function initAlbumGridTap(){
  const grid=document.getElementById('image-grid');
  if(!grid||grid.dataset.albumTap) return;
  grid.dataset.albumTap='1';
  grid.addEventListener('click',albumGridTap);
}

let albums=[], currentAlbumId='all', draftFilter=emptyAlbumFilter(), knownTags=[];
let albumReorderBusy=false, albumDragId=null, albumFilterEditing=false;
let albumTagAddMode='all';
const albumFormatOptions=['4:3','3:4','16:9','9:16'];

function emptyAlbumFilter(){
  return {tags_all:[],tags_any:[],tags_none:[],orientation:'',formats_any:[],no_saved_crops:null,untagged:null,exclude_ids:[]};
}

function cloneAlbumFilter(f){
  return JSON.parse(JSON.stringify(f||emptyAlbumFilter()));
}

function parseAlbumFilterJSON(raw){
  try{
    if(!raw||raw==='{}') return emptyAlbumFilter();
    const f=JSON.parse(raw);
    return {
      tags_all:f.tags_all||[],
      tags_any:f.tags_any||[],
      tags_none:f.tags_none||[],
      orientation:f.orientation||'',
      formats_any:f.formats_any||[],
      no_saved_crops:f.no_saved_crops===true?true:null,
      untagged:f.untagged===true?true:null,
      exclude_ids:f.exclude_ids||[]
    };
  }catch(e){
    return emptyAlbumFilter();
  }
}

function albumFilterActive(f){
  f=f||draftFilter;
  return !!(f.tags_all&&f.tags_all.length)||!!(f.tags_any&&f.tags_any.length)||!!(f.tags_none&&f.tags_none.length)||
    !!f.orientation||!!(f.formats_any&&f.formats_any.length)||f.no_saved_crops===true||f.untagged===true||
    !!(f.exclude_ids&&f.exclude_ids.length);
}

function currentAlbumMeta(){
  return albums.find(a=>a.id===currentAlbumId);
}

function isAllPhotosAlbum(){
  return currentAlbumId==='all';
}

function isSmartAlbumView(){
  const a=currentAlbumMeta();
  return !!(a&&a.kind==='smart');
}

function buildImagesURL(){
  if(!isAllPhotosAlbum()){
    return '/api/albums/'+encodeURIComponent(currentAlbumId)+'/images';
  }
  const f=draftFilter;
  const p=new URLSearchParams();
  (f.tags_all||[]).forEach(t=>p.append('tag',t));
  (f.tags_any||[]).forEach(t=>p.append('tag_any',t));
  (f.tags_none||[]).forEach(t=>p.append('tag_none',t));
  if(f.orientation) p.set('orientation',f.orientation);
  (f.formats_any||[]).forEach(fmt=>p.append('format',fmt));
  if(f.no_saved_crops===true) p.set('no_saved_crops','1');
  if(f.untagged===true) p.set('untagged','1');
  (f.exclude_ids||[]).forEach(id=>p.append('exclude',id));
  const q=p.toString();
  return '/api/images'+(q?'?'+q:'');
}

async function loadAlbums(){
  try{
    const r=await fetch('/api/albums');
    if(!r.ok) return;
    albums=await r.json()||[];
    renderAlbumSidebar();
  }catch(e){}
}

async function loadAllTags(){
  try{
    const r=await fetch('/api/tags');
    if(!r.ok) return;
    knownTags=await r.json()||[];
    renderAlbumToolbar();
  }catch(e){}
}

function renderAlbumSidebar(){
  const nav=document.getElementById('album-nav');
  if(!nav) return;
  const smart=albums.filter(a=>a.kind==='smart');
  let html='<li><button type="button" class="album-nav-btn'+(currentAlbumId==='all'?' active':'')+'" onclick="selectAlbum(\'all\')">All photos</button></li>';
  smart.forEach(a=>{
    const name=escHtml(a.name||a.id);
    html+='<li><button type="button" class="album-nav-btn'+(currentAlbumId===a.id?' active':'')+'" onclick="selectAlbum(\''+a.id+'\')">'+name+'</button></li>';
  });
  nav.innerHTML=html;
}

function selectAlbum(id){
  currentAlbumId=id||'all';
  albumFilterEditing=false;
  if(isAllPhotosAlbum()){
    /* keep draftFilter for unsaved browse filters */
  }else{
    const a=currentAlbumMeta();
    draftFilter=parseAlbumFilterJSON(a&&a.filter_json);
  }
  renderAlbumSidebar();
  renderAlbumToolbar();
  loadImages();
}

function toggleAlbumFormat(fmt){
  if(!draftFilter.formats_any) draftFilter.formats_any=[];
  const i=draftFilter.formats_any.indexOf(fmt);
  if(i>=0) draftFilter.formats_any.splice(i,1);
  else draftFilter.formats_any.push(fmt);
  renderAlbumToolbar();
}

function addDraftTag(){
  const input=document.getElementById('album-tag-input');
  if(!input) return;
  const t=input.value.trim();
  if(!t) return;
  const modeSel=document.getElementById('album-tag-mode');
  if(modeSel) albumTagAddMode=modeSel.value;
  const key=albumTagModeKey(albumTagAddMode);
  if(!draftFilter[key]) draftFilter[key]=[];
  removeDraftTagFromAll(t);
  if(!draftFilter[key].includes(t)) draftFilter[key].push(t);
  input.value='';
  renderAlbumToolbar();
}

function albumTagModeKey(mode){
  if(mode==='any') return 'tags_any';
  if(mode==='none') return 'tags_none';
  return 'tags_all';
}

function removeDraftTagFromAll(tag){
  draftFilter.tags_all=(draftFilter.tags_all||[]).filter(t=>t!==tag);
  draftFilter.tags_any=(draftFilter.tags_any||[]).filter(t=>t!==tag);
  draftFilter.tags_none=(draftFilter.tags_none||[]).filter(t=>t!==tag);
}

function removeDraftTag(tag, mode){
  const key=albumTagModeKey(mode||'all');
  draftFilter[key]=(draftFilter[key]||[]).filter(t=>t!==tag);
  renderAlbumToolbar();
  reloadDraftFilterView();
}

function renderDraftTagChips(f, editable){
  const specs=[
    {mode:'all', list:f.tags_all||[], cls:'album-chip-tag-all', prefix:'', title:'must have'},
    {mode:'any', list:f.tags_any||[], cls:'album-chip-tag-any', prefix:'any ', title:'any of'},
    {mode:'none', list:f.tags_none||[], cls:'album-chip-tag-none', prefix:'not ', title:'must not'},
  ];
  return specs.map(spec=>spec.list.map(t=>{
    const label=spec.prefix+escHtml(t);
    if(editable){
      return '<span class="album-chip '+spec.cls+'" title="'+spec.title+'">'+label+
        ' <button type="button" aria-label="Remove tag" onclick=\'removeDraftTag('+JSON.stringify(t)+','+JSON.stringify(spec.mode)+')\'>&times;</button></span>';
    }
    return '<span class="album-chip '+spec.cls+'" title="'+spec.title+'">'+label+'</span>';
  }).join('')).join('');
}

function excludeChipLabel(id){
  const img=images.find(i=>i.id===id);
  if(img) return img.name||id.slice(0,8);
  return id.slice(0,8)+'…';
}

function canExcludeFromFilter(){
  return isAllPhotosAlbum()||isSmartAlbumView();
}

async function reloadDraftFilterView(){
  if(isAllPhotosAlbum()) await loadImages();
  else if(isSmartAlbumView()) await updateSmartAlbumFilter(false);
}

async function addDraftExclude(imageId){
  if(!imageId) return;
  if(!draftFilter.exclude_ids) draftFilter.exclude_ids=[];
  if(draftFilter.exclude_ids.includes(imageId)) return;
  draftFilter.exclude_ids.push(imageId);
  renderAlbumToolbar();
  await reloadDraftFilterView();
}

async function removeDraftExclude(imageId){
  draftFilter.exclude_ids=(draftFilter.exclude_ids||[]).filter(id=>id!==imageId);
  renderAlbumToolbar();
  await reloadDraftFilterView();
}

function applyAlbumFilters(){
  if(isSmartAlbumView()&&!albumFilterEditing) return;
  if(isAllPhotosAlbum()) loadImages();
  else updateSmartAlbumFilter();
}

function clearAlbumFilters(){
  draftFilter=emptyAlbumFilter();
  albumFilterEditing=false;
  if(isAllPhotosAlbum()){
    renderAlbumToolbar();
    loadImages();
    return;
  }
  updateSmartAlbumFilter();
}

function renderAlbumToolbar(){
  const el=document.getElementById('album-toolbar');
  if(!el) return;
  const smart=isSmartAlbumView();
  const editable=isAllPhotosAlbum()||albumFilterEditing;
  const f=draftFilter;
  const chips=renderDraftTagChips(f, editable);
  const fmtRow=albumFormatOptions.map(fmt=>{
    const on=(f.formats_any||[]).includes(fmt);
    if(!editable){
      return on?'<span class="album-chip">'+escHtml(fmt)+'</span>':'';
    }
    return '<label><input type="checkbox"'+(on?' checked':'')+' onchange="toggleAlbumFormat('+JSON.stringify(fmt)+')"> '+escHtml(fmt)+'</label>';
  }).join('');
  const tagList=knownTags.map(t=>'<option value="'+escHtml(t)+'">').join('');
  let actions='';
  if(editable){
    actions+='<button type="button" class="album-tb-btn secondary" onclick="clearAlbumFilters()">Clear</button>';
    actions+='<button type="button" class="album-tb-btn" onclick="applyAlbumFilters()">Apply</button>';
    if(isAllPhotosAlbum()&&albumFilterActive()){
      actions+='<button type="button" class="album-tb-btn" onclick="saveDraftAsSmartAlbum()">Save as smart album</button>';
    }
    if(smart&&albumFilterEditing){
      actions+='<button type="button" class="album-tb-btn secondary" onclick="albumFilterEditing=false;selectAlbum(currentAlbumId)">Cancel</button>';
    }
  }else if(smart){
    actions+='<button type="button" class="album-tb-btn secondary" onclick="albumFilterEditing=true;renderAlbumToolbar()">Edit filters</button>';
    actions+='<button type="button" class="album-tb-btn secondary" onclick="renameSmartAlbum()">Rename</button>';
    actions+='<button type="button" class="album-tb-btn danger" onclick="deleteSmartAlbum()">Delete</button>';
  }
  const orientOpts=['','portrait','landscape','square'].map(o=>{
    const label=o||'Any orientation';
    return '<option value="'+o+'"'+(f.orientation===o?' selected':'')+'>'+label+'</option>';
  }).join('');
  const noCropsChip=f.no_saved_crops?'<span class="album-chip">No saved crops</span>':'';
  const noCropsEdit=editable?('<label><input type="checkbox"'+(f.no_saved_crops?' checked':'')+
    ' onchange="draftFilter.no_saved_crops=this.checked?true:null"> No saved crops</label>'):'';
  const untaggedChip=f.untagged?'<span class="album-chip">Untagged</span>':'';
  const untaggedEdit=editable?('<label><input type="checkbox"'+(f.untagged?' checked':'')+
    ' onchange="draftFilter.untagged=this.checked?true:null"> Untagged</label>'):'';
  const excludeChips=(f.exclude_ids||[]).map(id=>{
    const label=escHtml(excludeChipLabel(id));
    const title=' title="Except '+escHtml(id)+'"';
    if(editable){
      return '<span class="album-chip album-chip-exclude"'+title+'>'+label+
        ' <button type="button" aria-label="Remove exclusion" onclick=\'removeDraftExclude('+JSON.stringify(id)+')\'>&times;</button></span>';
    }
    return '<span class="album-chip album-chip-exclude"'+title+'>Except '+label+'</span>';
  }).join('');
  const tagModeOpts=[
    {v:'all', label:'must have (and)'},
    {v:'any', label:'any of (or)'},
    {v:'none', label:'must not (not)'},
  ].map(o=>'<option value="'+o.v+'"'+(albumTagAddMode===o.v?' selected':'')+'>'+o.label+'</option>').join('');
  const filterRow=editable?(
    '<label>Tag rule <select id="album-tag-mode" onchange="albumTagAddMode=this.value">'+tagModeOpts+'</select></label>'+
    '<label>Tag <input type="text" id="album-tag-input" list="album-tag-suggestions" placeholder="Add tag…" onkeydown="if(event.key===\'Enter\'){event.preventDefault();addDraftTag();}">'+
    '<datalist id="album-tag-suggestions">'+tagList+'</datalist></label>'+
    '<button type="button" class="album-tb-btn secondary" onclick="addDraftTag()">Add</button>'+
    '<label>Orientation <select id="album-orient" onchange="draftFilter.orientation=this.value">'+orientOpts+'</select></label>'+
    noCropsEdit+
    untaggedEdit+
  '<div class="album-chip-row">'+fmtRow+'</div>'
  ):'';
  const title=smart&&!editable?('<strong style="margin-right:8px">'+escHtml(currentAlbumMeta().name||'')+'</strong>'):'';
  el.innerHTML='<div class="album-toolbar-inner">'+
    title+
    (chips||excludeChips||noCropsChip||untaggedChip?'<div class="album-chip-row">'+chips+excludeChips+noCropsChip+untaggedChip+'</div>':'')+
    filterRow+
    (actions?'<div class="album-toolbar-actions">'+actions+'</div>':'')+
    '</div>';
}

async function saveDraftAsSmartAlbum(){
  const name=prompt('Smart album name');
  if(!name||!name.trim()) return;
  try{
    const r=await fetch('/api/albums',{
      method:'POST',
      headers:{'Content-Type':'application/json'},
      body:JSON.stringify({name:name.trim(),filter:draftFilter})
    });
    if(!r.ok) throw new Error(await r.text());
    const a=await r.json();
    await loadAlbums();
    selectAlbum(a.id);
  }catch(e){
    alert('Create album failed: '+e.message);
  }
}

async function updateSmartAlbumFilter(finishEdit){
  if(finishEdit===undefined) finishEdit=true;
  if(!isSmartAlbumView()) return;
  try{
    const r=await fetch('/api/albums/'+encodeURIComponent(currentAlbumId),{
      method:'PATCH',
      headers:{'Content-Type':'application/json'},
      body:JSON.stringify({filter:draftFilter})
    });
    if(!r.ok) throw new Error(await r.text());
    if(finishEdit) albumFilterEditing=false;
    await loadAlbums();
    draftFilter=parseAlbumFilterJSON(currentAlbumMeta()?.filter_json);
    renderAlbumSidebar();
    renderAlbumToolbar();
    await loadImages();
  }catch(e){
    alert('Update album failed: '+e.message);
  }
}

async function renameSmartAlbum(){
  const a=currentAlbumMeta();
  if(!a) return;
  const name=prompt('Album name', a.name||'');
  if(!name||!name.trim()||name.trim()===a.name) return;
  try{
    const r=await fetch('/api/albums/'+encodeURIComponent(currentAlbumId),{
      method:'PATCH',
      headers:{'Content-Type':'application/json'},
      body:JSON.stringify({name:name.trim()})
    });
    if(!r.ok) throw new Error(await r.text());
    await loadAlbums();
    renderAlbumSidebar();
    renderAlbumToolbar();
  }catch(e){
    alert('Rename failed: '+e.message);
  }
}

async function deleteSmartAlbum(){
  const a=currentAlbumMeta();
  if(!a) return;
  if(!confirm('Delete smart album “'+(a.name||a.id)+'”? Photos are not deleted.')) return;
  try{
    const r=await fetch('/api/albums/'+encodeURIComponent(currentAlbumId),{method:'DELETE'});
    if(!r.ok) throw new Error(await r.text());
    currentAlbumId='all';
    draftFilter=emptyAlbumFilter();
    albumFilterEditing=false;
    await loadAlbums();
    selectAlbum('all');
  }catch(e){
    alert('Delete album failed: '+e.message);
  }
}

async function createSmartAlbum(){
  if(albumFilterActive()){
    await saveDraftAsSmartAlbum();
    return;
  }
  const name=prompt('Smart album name');
  if(!name||!name.trim()) return;
  try{
    const r=await fetch('/api/albums',{
      method:'POST',
      headers:{'Content-Type':'application/json'},
      body:JSON.stringify({name:name.trim(),filter:emptyAlbumFilter()})
    });
    if(!r.ok) throw new Error(await r.text());
    const a=await r.json();
    await loadAlbums();
    selectAlbum(a.id);
    albumFilterEditing=true;
    renderAlbumToolbar();
  }catch(e){
    alert('Create album failed: '+e.message);
  }
}

function updateAlbumReorderHint(){
  const hint=document.getElementById('album-reorder-hint');
  if(!hint) return;
  const show=images.length>1&&!albumCaptionEditingId;
  hint.hidden=!show;
}

function setupAlbumDrag(card, imageId){
  if(card.dataset.albumDrag){
    card.draggable=images.length>=2;
    return;
  }
  card.dataset.albumDrag='1';
  card.addEventListener('dragstart',e=>{
    if(albumCaptionEditingId||albumReorderBusy){
      e.preventDefault();
      return;
    }
    albumDragId=imageId;
    e.dataTransfer.effectAllowed='move';
    card.classList.add('album-dragging');
  });
  card.addEventListener('dragend',()=>{
    card.classList.remove('album-dragging');
    document.querySelectorAll('.album-drop-before').forEach(el=>el.classList.remove('album-drop-before'));
    albumDragId=null;
  });
  card.addEventListener('dragover',e=>{
    if(!albumDragId||albumDragId===imageId) return;
    const rect=card.getBoundingClientRect();
    if(e.clientX>rect.left+rect.width*0.35) return;
    e.preventDefault();
    e.dataTransfer.dropEffect='move';
    document.querySelectorAll('.album-drop-before').forEach(el=>el.classList.remove('album-drop-before'));
    card.classList.add('album-drop-before');
  });
  card.addEventListener('dragleave',e=>{
    if(!card.contains(e.relatedTarget)) card.classList.remove('album-drop-before');
  });
  card.addEventListener('drop',async e=>{
    e.preventDefault();
    e.stopPropagation();
    card.classList.remove('album-drop-before');
    const draggedId=albumDragId;
    if(!draggedId||draggedId===imageId) return;
    const rect=card.getBoundingClientRect();
    if(e.clientX>rect.left+rect.width*0.35) return;
    await saveAlbumMove(draggedId, imageId);
  });
  card.draggable=images.length>=2;
}

async function saveAlbumMove(draggedId, targetId){
  if(draggedId===targetId) return;
  albumReorderBusy=true;
  const prev=images.slice();
  const from=prev.findIndex(i=>i.id===draggedId);
  const to=prev.findIndex(i=>i.id===targetId);
  if(from<0||to<0){ albumReorderBusy=false; return; }
  const next=prev.slice();
  const item=next.splice(from,1)[0];
  let insertAt=to;
  if(from<to) insertAt--;
  next.splice(insertAt,0,item);
  images=next;
  renderAlbumGrid();
  try{
    const r=await fetch('/api/albums/'+encodeURIComponent(currentAlbumId)+'/order',{
      method:'PATCH',
      headers:{'Content-Type':'application/json'},
      body:JSON.stringify({move:{id:draggedId,target:targetId}})
    });
    if(!r.ok) throw new Error(await r.text());
  }catch(e){
    images=prev;
    renderAlbumGrid();
    alert('Reorder failed: '+e.message);
  }finally{
    albumReorderBusy=false;
  }
}

async function editAlbumTags(imageId){
  const img=images.find(i=>i.id===imageId);
  const cur=(img&&img.tags)?img.tags.join(', '):'';
  const s=prompt('Tags (comma-separated)', cur);
  if(s===null) return;
  const tags=s.split(',').map(t=>t.trim()).filter(Boolean);
  try{
    const r=await fetch('/api/images/'+imageId,{
      method:'PATCH',
      headers:{'Content-Type':'application/json'},
      body:JSON.stringify({tags})
    });
    if(!r.ok) throw new Error(await r.text());
    const meta=await r.json();
    if(img) img.tags=meta.tags||[];
    renderAlbumGrid();
    loadAllTags();
  }catch(e){
    alert('Tags update failed: '+e.message);
  }
}

function renderAlbumGrid(){
  const el=document.getElementById('image-grid');
  if(!el) return;
  if(!images||!images.length){
    albumSelectedId=null;
    if(albumZoomId) closeAlbumZoom();
    const msg=albumFilterActive()?'No photos match these filters.':'No images uploaded yet.';
    el.innerHTML='<p class="album-empty">'+msg+'</p>';
    updateAlbumReorderHint();
    return;
  }
  el.innerHTML=images.map((img,i)=>{
    const name=escHtml(img.name);
    const tags=(img.tags&&img.tags.length)?'<div class="album-tags" title="'+escHtml(img.tags.join(', '))+'">'+escHtml(img.tags.join(' · '))+'</div>':'';
    const sz=albumPhotoSize(img);
    const excludeBtn=canExcludeFromFilter()?
      '<button type="button" class="album-btn album-btn-exclude" onclick="event.stopPropagation();addDraftExclude(\''+img.id+'\')">Exclude</button>':'';
    return '<div class="album-outer'+(sz.portrait?' album-outer-portrait':'')+'" id="card-'+img.id+'" style="'+albumStackVars(i)+'">'+
      '<div class="album-print" style="--photo-w:'+sz.w+'px;--photo-h:'+sz.h+'px">'+
        '<div class="album-img">'+
          '<img src="/images/'+img.id+'/preview" alt="'+name+'" loading="lazy">'+
        '</div>'+
        '<div class="album-menu">'+
          excludeBtn+
          '<button type="button" class="album-btn" onclick="event.stopPropagation();editAlbumTags(\''+img.id+'\')">Tags</button>'+
          '<button type="button" class="album-btn" onclick="event.stopPropagation();openCrop(\''+img.id+'\')">Frame</button>'+
          '<button type="button" class="album-btn" onclick="event.stopPropagation();downloadAlbumImage(\''+img.id+'\','+JSON.stringify(img.name)+')">Save</button>'+
          '<button type="button" class="album-btn" id="send-btn-'+img.id+'" onclick="sendImageToFrame(event,\''+img.id+'\')">Send</button>'+
          '<button type="button" class="album-btn album-btn-delete" onclick="event.stopPropagation();deleteImg(\''+img.id+'\')">Delete</button>'+
        '</div>'+
        '<div class="album-caption-wrap">'+
          '<div class="album-caption" title="'+name+'">'+name+'</div>'+
        '</div>'+tags+
      '</div></div>';
  }).join('');
  initAlbumGridTap();
  images.forEach(img=>{
    const card=document.getElementById('card-'+img.id);
    if(card) setupAlbumDrag(card, img.id);
  });
  if(albumSelectedId){
    if(images.some(img=>img.id===albumSelectedId)){
      const sel=document.getElementById('card-'+albumSelectedId);
      if(sel) sel.classList.add('album-outer-selected');
    }else{
      albumSelectedId=null;
    }
  }
  if(albumZoomId&&!images.some(img=>img.id===albumZoomId)) closeAlbumZoom();
  updateAlbumReorderHint();
}

async function loadImages(){
  const r=await fetch(buildImagesURL());
  images=await r.json();
  images.forEach(img=>{ imageCropsCache[img.id]=img.crops||{}; });
  renderAlbumGrid();
  renderAlbumToolbar();
}

