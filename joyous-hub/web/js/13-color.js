// ── Color tab ───────────────────────────────────────────────────────────────
let colorConfig=null, colorPresets=null;
const COLOR_PRESET_LABELS={
  calibrated:'Calibrated (on-panel P2)',
  legacy:'Legacy physical ink',
  srgb:'Pure sRGB primaries',
  reflection:'Reflection Spectra 6',
  custom:'Custom'
};
const COLOR_GROUPS=[
  {key:'inkjoy_display',presetId:'clr-inkjoy-display-preset',swatchId:'clr-inkjoy-display-swatches',cfgPreset:'inkjoy_display_preset',cfgRGB:'inkjoy_display'},
  {key:'samsung_display',presetId:'clr-samsung-display-preset',swatchId:'clr-samsung-display-swatches',cfgPreset:'samsung_display_preset',cfgRGB:'samsung_display'},
  {key:'samsung_send',presetId:'clr-samsung-send-preset',swatchId:'clr-samsung-send-swatches',cfgPreset:'samsung_send_preset',cfgRGB:'samsung_send'}
];

function fillColorPresetSelect(sel){
  sel.innerHTML=Object.entries(COLOR_PRESET_LABELS).map(([v,l])=>'<option value="'+v+'">'+l+'</option>').join('');
}

function renderColorSwatches(groupKey, containerId){
  const el=document.getElementById(containerId);
  const names=colorPresets&&colorPresets.names?colorPresets.names:['black','white','yellow','red','blue','green'];
  el.innerHTML='<table style="width:100%;font-size:.8rem;border-collapse:collapse;margin:.35rem 0 .75rem"><thead><tr><th style="text-align:left;padding:.2rem 0">Ink</th><th>R</th><th>G</th><th>B</th><th></th></tr></thead><tbody>'+
    names.map((n,i)=>{
      const id=groupKey+'-'+i;
      return '<tr><td style="padding:.2rem .35rem .2rem 0">'+n+'</td>'+
        '<td><input id="clr-'+id+'-r" type="number" min="0" max="255" style="width:3.2rem;padding:.2rem;border:1px solid #ccc;border-radius:3px"></td>'+
        '<td><input id="clr-'+id+'-g" type="number" min="0" max="255" style="width:3.2rem;padding:.2rem;border:1px solid #ccc;border-radius:3px"></td>'+
        '<td><input id="clr-'+id+'-b" type="number" min="0" max="255" style="width:3.2rem;padding:.2rem;border:1px solid #ccc;border-radius:3px"></td>'+
        '<td><span id="clr-'+id+'-sw" style="display:inline-block;width:1.4rem;height:1.1rem;border:1px solid #ccc;border-radius:3px;vertical-align:middle"></span></td></tr>';
    }).join('')+'</tbody></table>';
}

function readPaletteGroup(groupKey){
  const names=colorPresets&&colorPresets.names?colorPresets.names:[];
  const out=[];
  for(let i=0;i<6;i++){
    const r=parseInt(document.getElementById('clr-'+groupKey+'-'+i+'-r').value,10)||0;
    const g=parseInt(document.getElementById('clr-'+groupKey+'-'+i+'-g').value,10)||0;
    const b=parseInt(document.getElementById('clr-'+groupKey+'-'+i+'-b').value,10)||0;
    out.push([Math.max(0,Math.min(255,r)),Math.max(0,Math.min(255,g)),Math.max(0,Math.min(255,b))]);
    const sw=document.getElementById('clr-'+groupKey+'-'+i+'-sw');
    if(sw) sw.style.background='rgb('+out[i].join(',')+')';
  }
  return out;
}

function writePaletteGroup(groupKey, palette){
  if(!palette) return;
  for(let i=0;i<6;i++){
    const rgb=palette[i]||[0,0,0];
    const r=document.getElementById('clr-'+groupKey+'-'+i+'-r');
    const g=document.getElementById('clr-'+groupKey+'-'+i+'-g');
    const b=document.getElementById('clr-'+groupKey+'-'+i+'-b');
    if(r){r.value=rgb[0];g.value=rgb[1];b.value=rgb[2];}
  }
  readPaletteGroup(groupKey);
}

function onColorPresetChange(groupKey){
  const g=COLOR_GROUPS.find(x=>x.key===groupKey);
  if(!g||!colorPresets) return;
  const preset=document.getElementById(g.presetId).value;
  if(preset!=='custom'){
    writePaletteGroup(groupKey, colorPresets.presets[groupKey][preset]);
  }
}

function applyColorForm(cfg){
  document.getElementById('clr-lab-chroma').checked=!!cfg.lab_chroma_enabled;
  const chromaStrength=cfg.lab_chroma_strength||1;
  document.getElementById('clr-lab-chroma-strength').value=chromaStrength;
  document.getElementById('clr-lab-chroma-strength-val').textContent=String(chromaStrength);
  document.getElementById('clr-lab-dynamic-range').checked=!!cfg.lab_dynamic_range_enabled;
  const drStops=cfg.lab_dynamic_range_stops||4.5;
  document.getElementById('clr-lab-dynamic-range-stops').value=drStops;
  document.getElementById('clr-lab-dynamic-range-stops-val').textContent=String(drStops);
  document.getElementById('clr-lab-highlight').checked=!!cfg.lab_highlight_enabled;
  const highlightStrength=cfg.lab_highlight_strength||1;
  document.getElementById('clr-lab-highlight-strength').value=highlightStrength;
  document.getElementById('clr-lab-highlight-strength-val').textContent=String(highlightStrength);
  document.getElementById('clr-lab-shadow').checked=!!cfg.lab_shadow_enabled;
  const shadowStrength=cfg.lab_shadow_strength||1;
  document.getElementById('clr-lab-shadow-strength').value=shadowStrength;
  document.getElementById('clr-lab-shadow-strength-val').textContent=String(shadowStrength);
  document.getElementById('clr-lab-skin-tone').checked=cfg.lab_skin_tone_enabled!==false;
  const skinStrength=cfg.lab_skin_tone_strength||1;
  document.getElementById('clr-lab-skin-tone-strength').value=skinStrength;
  document.getElementById('clr-lab-skin-tone-strength-val').textContent=String(skinStrength);
  document.getElementById('clr-lab-ink-affinity').checked=cfg.lab_ink_affinity_enabled!==false;
  const inkAffinityStrength=cfg.lab_ink_affinity_strength||1;
  document.getElementById('clr-lab-ink-affinity-strength').value=inkAffinityStrength;
  document.getElementById('clr-lab-ink-affinity-strength-val').textContent=String(inkAffinityStrength);
  document.getElementById('clr-lab-ink-affinity-mix').checked=!!cfg.lab_ink_affinity_mix_enabled;
  const inkMixStrength=cfg.lab_ink_affinity_mix_strength||1;
  document.getElementById('clr-lab-ink-affinity-mix-strength').value=inkMixStrength;
  document.getElementById('clr-lab-ink-affinity-mix-strength-val').textContent=String(inkMixStrength);
  document.getElementById('clr-lab-ink-affinity-ray').checked=!!cfg.lab_ink_affinity_ray_enabled;
  const inkRayStrength=cfg.lab_ink_affinity_ray_strength||1;
  document.getElementById('clr-lab-ink-affinity-ray-strength').value=inkRayStrength;
  document.getElementById('clr-lab-ink-affinity-ray-strength-val').textContent=String(inkRayStrength);
  fillInkJoyWipeSelect(cfg.inkjoy_wipe||'uniform:248');
  applyInkJoyUniformWipe(cfg.inkjoy_wipe||'uniform:248');
  fillInkJoy28WipeSelect(cfg.inkjoy28_wipe||'off');
  for(const g of COLOR_GROUPS){
    document.getElementById(g.presetId).value=cfg[g.cfgPreset]||'calibrated';
    const preset=cfg[g.cfgPreset]||'calibrated';
    const pal=preset==='custom'?cfg[g.cfgRGB]:(colorPresets&&colorPresets.presets[g.key][preset]);
    writePaletteGroup(g.key, pal);
  }
}

function colorFormValues(){
  const out={
    lab_chroma_enabled:document.getElementById('clr-lab-chroma').checked,
    lab_chroma_strength:parseFloat(document.getElementById('clr-lab-chroma-strength').value)||1,
    lab_dynamic_range_enabled:document.getElementById('clr-lab-dynamic-range').checked,
    lab_dynamic_range_stops:parseFloat(document.getElementById('clr-lab-dynamic-range-stops').value)||4.5,
    lab_highlight_enabled:document.getElementById('clr-lab-highlight').checked,
    lab_highlight_strength:parseFloat(document.getElementById('clr-lab-highlight-strength').value)||1,
    lab_shadow_enabled:document.getElementById('clr-lab-shadow').checked,
    lab_shadow_strength:parseFloat(document.getElementById('clr-lab-shadow-strength').value)||1,
    lab_skin_tone_enabled:document.getElementById('clr-lab-skin-tone').checked,
    lab_skin_tone_strength:parseFloat(document.getElementById('clr-lab-skin-tone-strength').value)||1,
    lab_ink_affinity_enabled:document.getElementById('clr-lab-ink-affinity').checked,
    lab_ink_affinity_strength:parseFloat(document.getElementById('clr-lab-ink-affinity-strength').value)||1,
    lab_ink_affinity_mix_enabled:document.getElementById('clr-lab-ink-affinity-mix').checked,
    lab_ink_affinity_mix_strength:parseFloat(document.getElementById('clr-lab-ink-affinity-mix-strength').value)||1,
    lab_ink_affinity_ray_enabled:document.getElementById('clr-lab-ink-affinity-ray').checked,
    lab_ink_affinity_ray_strength:parseFloat(document.getElementById('clr-lab-ink-affinity-ray-strength').value)||1,
    inkjoy_wipe:inkjoyWipeFormValue(),
    inkjoy28_wipe:(document.getElementById('clr-inkjoy28-wipe')||{}).value||'off',
    inkjoy_display_preset:document.getElementById('clr-inkjoy-display-preset').value,
    samsung_display_preset:document.getElementById('clr-samsung-display-preset').value,
    samsung_send_preset:document.getElementById('clr-samsung-send-preset').value
  };
  for(const g of COLOR_GROUPS){
    out[g.cfgRGB]=readPaletteGroup(g.key);
    if(document.getElementById(g.presetId).value==='custom'){
      out[g.cfgPreset]='custom';
    }
  }
  return out;
}

async function loadColorConfig(){
  const r=await fetch('/api/color');
  if(!r.ok) throw new Error(await r.text());
  colorConfig=await r.json();
}

async function loadColorPresets(){
  const r=await fetch('/api/color/presets');
  if(!r.ok) throw new Error(await r.text());
  colorPresets=await r.json();
}

function fillInkJoy28WipeSelect(selected){
  const sel=document.getElementById('clr-inkjoy28-wipe');
  if(!sel) return;
  const wipes=colorPresets&&colorPresets.wipes28?colorPresets.wipes28:[{id:'off',label:'Off (no transition)'}];
  sel.innerHTML='';
  for(const w of wipes){
    const o=document.createElement('option');
    o.value=w.id;
    o.textContent=w.label;
    sel.appendChild(o);
  }
  if(selected&&!wipes.some(w=>w.id===selected)){
    const o=document.createElement('option');
    o.value=selected;
    o.textContent=selected+' (missing)';
    sel.appendChild(o);
  }
  sel.value=selected||'off';
}

function fillInkJoyWipeSelect(selected){
  const sel=document.getElementById('clr-inkjoy-wipe');
  if(!sel) return;
  const wipes=colorPresets&&colorPresets.wipes?colorPresets.wipes:[
    {id:'uniform:picker',label:'Uniform lo (consistent)'},
    {id:'random',label:'Random pattern'}
  ];
  sel.innerHTML='';
  for(const w of wipes){
    const o=document.createElement('option');
    o.value=w.id;
    o.textContent=w.label;
    sel.appendChild(o);
  }
  if(isUniformInkJoyWipe(selected)){
    sel.value='uniform:picker';
  }else if(selected){
    sel.value=selected;
  }
  onInkJoyWipePatternChange();
}

function isUniformInkJoyWipe(id){
  return !id||id==='uniform248'||id.startsWith('uniform:');
}

function uniformLoFromStep(step){
  const s=Math.max(0,Math.min(30,parseInt(step,10)||0));
  return s>=30?248:s*8;
}

function uniformStepFromLo(lo){
  const n=parseInt(lo,10);
  if(isNaN(n)||n>=248) return 30;
  return Math.max(0,Math.min(30,Math.round(n/8)));
}

function updateInkJoyUniformLoLabel(){
  const step=document.getElementById('clr-inkjoy-uniform-step').value;
  const lo=uniformLoFromStep(step);
  document.getElementById('clr-inkjoy-uniform-step-val').textContent='step '+step+' · lo '+lo;
}

function onInkJoyWipePatternChange(){
  const wrap=document.getElementById('clr-inkjoy-uniform-wrap');
  const uniform=document.getElementById('clr-inkjoy-wipe').value==='uniform:picker';
  if(wrap) wrap.style.display=uniform?'block':'none';
}

function applyInkJoyUniformWipe(inkjoyWipe){
  let step=30;
  if(inkjoyWipe==='uniform248'||inkjoyWipe==='uniform:248') step=30;
  else if(inkjoyWipe&&inkjoyWipe.startsWith('uniform:')){
    step=uniformStepFromLo(inkjoyWipe.split(':')[1]);
  }
  document.getElementById('clr-inkjoy-uniform-step').value=String(step);
  updateInkJoyUniformLoLabel();
}

function inkjoyWipeFormValue(){
  const pattern=document.getElementById('clr-inkjoy-wipe').value;
  if(pattern==='uniform:picker'){
    const lo=uniformLoFromStep(document.getElementById('clr-inkjoy-uniform-step').value);
    return 'uniform:'+lo;
  }
  return pattern||'uniform:248';
}

async function loadColorTab(){
  try{
    if(!colorPresets) await loadColorPresets();
    for(const g of COLOR_GROUPS){
      fillColorPresetSelect(document.getElementById(g.presetId));
      renderColorSwatches(g.key, g.swatchId);
    }
    if(!colorConfig) await loadColorConfig();
    applyColorForm(colorConfig||{});
  }catch(e){
    document.getElementById('clr-save-status').textContent='Load failed: '+e.message;
  }
}

async function saveColorConfig(){
  const st=document.getElementById('clr-save-status');
  st.textContent='Saving…';
  try{
    const body=colorFormValues();
    for(const g of COLOR_GROUPS){
      if(document.getElementById(g.presetId).value!=='custom'){
        body[g.cfgPreset]=document.getElementById(g.presetId).value;
      }else{
        body[g.cfgPreset]='custom';
      }
    }
    const r=await fetch('/api/color',{
      method:'PUT',
      headers:{'Content-Type':'application/json'},
      body:JSON.stringify(body)
    });
    if(!r.ok) throw new Error(await r.text());
    colorConfig=body;
    st.style.color='#28a745';
    st.textContent='Saved. Bin cache cleared — re-send images to apply.';
  }catch(e){
    st.style.color='#dc3545';
    st.textContent='Save failed: '+e.message;
  }
}

