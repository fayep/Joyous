//go:build nixplaybridge

package main

// nixplayBridgePageHTML is the bridge-owned Nixplay configuration page.
// API calls use relative paths under /nixplay/api/… (hub proxies /nixplay/… over MQTT).
const nixplayBridgePageHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<base href="/nixplay/">
<title>Nixplay</title>
<style>
*{box-sizing:border-box}
body{font-family:system-ui,-apple-system,sans-serif;margin:0;padding:1rem 1.25rem;background:#f8f9fa;color:#222}
h3{margin:0 0 .5rem}
p.hint{color:#666;font-size:.85rem;margin:.25rem 0 1rem}
.card{background:#fff;border:1px solid #ddd;border-radius:8px;padding:1rem;margin-bottom:1rem}
.gallery-row{display:flex;align-items:center;gap:.6rem;padding:.5rem .25rem;border-bottom:1px solid #eee}
.gallery-row:last-child{border-bottom:none}
.gallery-name{font-weight:500}
.gallery-count{color:#888;font-size:.85rem;margin-left:.4rem}
.gallery-row label{display:flex;align-items:center;gap:.4rem;margin-left:auto;font-size:.85rem;color:#555;cursor:pointer;white-space:nowrap}
.empty{color:#888;font-size:.9rem}
.error{color:#dc3545;font-size:.9rem}
</style>
</head>
<body>
<h3>Nixplay galleries</h3>
<p class="hint">Hidden galleries stay in your Nixplay account but won't show up as a Send target or in Devices on this hub.</p>
<div class="card">
  <div id="gallery-list"><p class="empty">Loading…</p></div>
</div>
<script>
async function loadGalleries(){
  const el=document.getElementById('gallery-list');
  try{
    const r=await fetch('/nixplay/api/galleries');
    if(!r.ok) throw new Error(await r.text());
    const galleries=await r.json();
    if(!galleries||!galleries.length){ el.innerHTML='<p class="empty">No galleries found.</p>'; return; }
    el.innerHTML=galleries.map(g=>{
      const id=escAttr(g.id);
      return '<div class="gallery-row">'+
        '<span class="gallery-name">'+escHtml(g.name)+'</span>'+
        '<span class="gallery-count">'+(g.picture_count||0)+' photo(s)</span>'+
        '<label><input type="checkbox" data-id="'+id+'" '+(g.hidden?'checked':'')+' onchange="toggleHidden(this)"> Hidden</label>'+
        '</div>';
    }).join('');
  }catch(e){
    el.innerHTML='<p class="error">Failed to load galleries: '+escHtml(e.message)+'</p>';
  }
}
async function toggleHidden(cb){
  const id=cb.dataset.id;
  cb.disabled=true;
  try{
    const r=await fetch('/nixplay/api/galleries/'+encodeURIComponent(id),{
      method:'PATCH',headers:{'Content-Type':'application/json'},
      body:JSON.stringify({hidden:cb.checked})
    });
    if(!r.ok) throw new Error(await r.text());
  }catch(e){
    alert('Failed to update: '+e.message);
    cb.checked=!cb.checked;
  }finally{
    cb.disabled=false;
  }
}
function escHtml(s){
  return String(s).replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;');
}
function escAttr(s){
  return String(s).replace(/&/g,'&amp;').replace(/"/g,'&quot;').replace(/'/g,'&#39;');
}
loadGalleries();
</script>
</body>
</html>
`
