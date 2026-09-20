"use strict";
const $ = (id) => document.getElementById(id);
let STATE = null;
let PREVIEW = null;

function esc(s){return String(s==null?"":s).replace(/[&<>"]/g,c=>({"&":"&amp;","<":"&lt;",">":"&gt;",'"':"&quot;"}[c]));}
function short(iri){const i=Math.max(iri.lastIndexOf("#"),iri.lastIndexOf("/"));return i<0?iri:iri.slice(i+1);}
function term(t){
  if(!t) return "—";
  if(t.kind==="iri") return `<span class="iri">${esc(short(t.value))}</span>`;
  if(t.kind==="bnode") return `<span class="muted">_:${esc(t.value)}</span>`;
  let facet="";
  if(t.lang) facet=` <span class="langtag">@${esc(t.lang)}</span>`;
  else if(t.datatype && !t.datatype.endsWith("#string")) facet=` <span class="dt">^^${esc(short(t.datatype))}</span>`;
  return `"${esc(t.value)}"${facet}`;
}
function graphName(g){return g?`<span class="tag">${esc(short(g))}</span>`:'<span class="tag">default</span>';}
function quadStr(q){return `${term(q.subject)} ${term(q.predicate)} ${term(q.object)} ${graphName(q.graph)}`;}
function toast(msg,bad){const t=$("toast");t.textContent=msg;t.style.borderColor=bad?"var(--bad)":"var(--line)";t.classList.add("show");setTimeout(()=>t.classList.remove("show"),4200);}

async function api(path,opts){
  const res=await fetch(path,opts);
  const data=await res.json().catch(()=>({}));
  if(!res.ok){throw Object.assign(new Error(data.error||res.statusText),{status:res.status,data});}
  return data;
}
const post=(path,body)=>api(path,{method:"POST",headers:{"Content-Type":"application/json"},body:JSON.stringify(body||{})});

async function loadState(){
  STATE=await api("/api/state");
  $("ver").textContent=STATE.version;
  $("rulefp").textContent=STATE.ruleFingerprint.slice(0,12);
  $("pubcount").textContent=STATE.publicationCount;
  renderCandidates();
  renderAffected();
}

const RELCLASS={
  "http://example.org/mapping#exact":["exact","exact"],
  "http://example.org/mapping#broader":["broader","broader"],
  "http://example.org/mapping#narrower":["narrower","narrower"],
  "http://example.org/mapping#oneToMany":["oneToMany","one-to-many"],
  "http://example.org/mapping#deprecated":["deprecated","deprecated"],
};
function decisionStatus(cv){
  if(cv.decision) return cv.decision.status;
  return "pending";
}

function renderCandidates(){
  const host=$("candidates");host.innerHTML="";
  STATE.candidates.forEach(cv=>{
    const c=cv.candidate;
    const [rc,rl]=RELCLASS[c.relation]||["","?"];
    const st=decisionStatus(cv);
    const div=document.createElement("div");div.className="cand";
    let branches="";
    if(c.branches&&c.branches.length){
      branches=`<div class="muted branch">分支判别：</div>`+c.branches.map(b=>{
        const conds=(b.conditions||[]).map(k=>`${esc(short(k.property))} ${k.operator.endsWith("present")?"存在":"缺失"}`).join(" 且 ");
        return `<div class="branch mono">→ <span class="iri">${esc(short(b.target))}</span> <span class="muted">当 ${conds||"无条件"}</span></div>`;
      }).join("");
    }
    const ev=(c.evidence||[]).map(e=>`<div class="evi">
        <b>${esc(e.basis||"证据")}</b>${e.detail?` — ${esc(e.detail)}`:""}
        ${e.sourceGraph?`<div class="muted">来源图 ${graphName({graph:e.sourceGraph})}</div>`:""}
        ${e.sourcePath?`<code>${esc(e.sourcePath)}</code>`:""}
      </div>`).join("");
    const rationale=cv.decision?`<div class="muted">决议理由：${esc(cv.decision.rationale||"(无)")} · 生效于版本 ${cv.decision.version}</div>`:"";
    div.innerHTML=`
      <div class="top">
        <div>
          <span class="tag">${c.sourceTermType.endsWith("Class")?"类":"属性"}</span>
          <span class="term">${term({kind:"iri",value:c.source})}</span>
          <span class="arrow"> ⟶ </span>
          ${c.relation.endsWith("deprecated")?'<span class="muted">废弃，无替代</span>':`<span class="term">${term({kind:"iri",value:c.target})}</span>`}
          ${branches}
        </div>
        <div class="row">
          <span class="rel ${rc}">${rl}</span>
          <span class="badge ${st}">${st}</span>
        </div>
      </div>
      <div class="muted" style="margin:4px 0">${esc(c.rationale||"")} <span class="tag">${esc(c.origin)}</span> <span class="tag">${esc(c.sourceVersion)}→${esc(c.targetVersion)}</span></div>
      ${rationale}
      <details><summary>候选证据 (${(c.evidence||[]).length})</summary>${ev||'<div class="muted">无</div>'}</details>
      <div class="row" style="margin-top:8px">
        <input placeholder="决议理由（可选）" data-role="reason" style="max-width:280px"/>
        <button data-act="accepted">接受</button>
        ${c.relation.endsWith("deprecated")?"":'<button class="warn" data-act="deprecated">标记废弃</button>'}
        <button class="bad" data-act="rejected">拒绝</button>
      </div>`;
    div.querySelectorAll("button[data-act]").forEach(btn=>{
      btn.onclick=()=>decide(c.id,btn.dataset.act,div.querySelector('[data-role=reason]').value);
    });
    host.appendChild(div);
  });
}

async function decide(id,status,reason){
  try{
    await post("/api/decide",{candidateId:id,status,rationale:reason,version:STATE.version});
    await loadState();
    await refreshPreview();
  }catch(e){
    if(e.status===409 && e.data.conflict){showConflict(e.data);return;}
    toast("决议失败: "+e.message,true);
  }
}

function showConflict(data){
  const box=$("conflictBox");
  const rows=(data.conflict||[]).map(cv=>{
    const c=cv.candidate;
    return `<tr><td class="term">${term({kind:"iri",value:c.source})}</td>
      <td>${esc(short(c.relation.split("#")[1]))} → ${c.target?esc(short(c.target)):"<废弃>"}</td>
      <td><span class="badge ${decisionStatus(cv)}">${decisionStatus(cv)}</span></td></tr>`;
  }).join("");
  box.innerHTML=`<section class="card full" style="margin:14px 14px 0"><div class="conflict">
    <b>并发冲突：</b>你基于过期映射版本操作，当前版本为 ${data.currentVersion}。请复议以下相关子图后重试：
    <table style="margin-top:8px"><thead><tr><th>源术语</th><th>候选</th><th>当前决议</th></tr></thead><tbody>${rows}</tbody></table>
    <div class="row" style="margin-top:8px"><button onclick="document.getElementById('conflictBox').innerHTML='';loadState()">我已查看，刷新状态</button></div>
  </div></section>`;
  toast(data.error,true);
}

function renderAffected(){
  const host=$("affected");
  if(!STATE.affected||!STATE.affected.length){host.innerHTML="";return;}
  host.innerHTML=`<div class="conflict"><b>新一轮自动建议影响了以下已接受决议，请复议（原决议未被覆盖）：</b>
    <table style="margin-top:6px"><tbody>${STATE.affected.map(a=>`<tr>
      <td class="term">${term({kind:"iri",value:a.source})}</td>
      <td>当前 <span class="iri">${esc(short(a.currentTarget||"—"))}</span>；新证据建议 <span class="iri">${esc(short(a.newTarget||"—"))}</span></td>
      <td class="muted">${esc(a.reason)}</td></tr>`).join("")}</tbody></table></div>`;
}

let ONTO=null, activeGraph="v1";
async function loadOntology(){
  ONTO=await api("/api/ontology");renderOntology();
}
function nodeLabel(idx,iri){
  if(idx.labels&&idx.labels[iri]){const l=idx.labels[iri];return `${short(iri)} "${l.value}"${l.lang?"@"+l.lang:""}`;}
  return short(iri);
}
function renderOntology(){
  // index: classes with subclass/labels/domain-range
  const g=ONTO[activeGraph];
  const bySub={};const typeOf={};
  g.quads.forEach(q=>{
    if(q.predicate.value==="http://www.w3.org/1999/02/22-rdf-syntax-ns#type"){(typeOf[q.subject.value]=typeOf[q.subject.value]||[]).push(q.object.value);}
    (bySub[q.subject.value]=bySub[q.subject.value]||[]).push(q);
  });
  let cls="",prop="";
  Object.keys(bySub).sort().forEach(s=>{
    const types=typeOf[s]||[];
    const isClass=types.some(t=>t.endsWith("#Class"));
    const quads=bySub[s];
    const labels=quads.filter(q=>q.predicate.value.endsWith("/label")).map(q=>`"${esc(q.object.value)}"${q.object.lang?'<span class="langtag">@'+esc(q.object.lang)+'</span>':""}`).join(" ");
    const lines=quads.map(q=>{
      const pv=short(q.predicate.value);
      if(pv==="type"||pv==="label")return "";
      let val=term(q.object);
      if(q.object.kind==="literal"&&(pv==="deprecated"))val=`<span class="tag ${q.object.value==="true"?"closed":""}">${esc(q.object.value)}</span>`;
      return `<div class="mono muted" style="padding-left:14px">${esc(pv)} ${val}</div>`;
    }).join("");
    const block=`<div class="cand" style="margin-bottom:6px">
      <div><b class="term">${term({kind:"iri",value:s})}</b> ${labels?`<span class="muted">${labels}</span>`:""}</div>${lines}</div>`;
    if(isClass)cls+=block; else prop+=block;
  });
  $("ontology").innerHTML=`<div class="muted" style="margin-bottom:6px">指纹 <span class="mono">${g.fingerprint.slice(0,12)}</span> · ${g.quads.length} 条</div>
    <h3 style="margin:6px 0;font-size:13px">类</h3>${cls||'<div class="muted">无</div>'}
    <h3 style="margin:10px 0 6px;font-size:13px">属性</h3>${prop||'<div class="muted">无</div>'}`;
}
document.querySelectorAll("[data-graph]").forEach(b=>b.onclick=()=>{activeGraph=b.dataset.graph;renderOntology();});

async function loadInstances(){
  const g=await api("/api/instances");
  $("instfp").textContent=g.fingerprint.slice(0,12);
  const bySub={};
  g.quads.forEach(q=>(bySub[q.subject.value]=bySub[q.subject.value]||[]).push(q));
  $("instances").innerHTML=Object.keys(bySub).sort().map(s=>{
    const qs=bySub[s];
    return `<details><summary><span class="term">${term(qs[0].subject)}</span> <span class="muted">${qs.length} 条</span></summary>
      <div class="path">${qs.map(q=>`<code>${quadStr(q)}</code>`).join("")}</div></details>`;
  }).join("");
}

const VCAT={
  missing_data:["miss","数据缺失"],
  datatype_mismatch:["dtype","datatype 不符"],
  closed_extra_predicate:["closed","闭集多出属性"],
  closed_set_extra_value:["enum","闭集多出取值"],
  cardinality:["dtype","基数冲突"],
  class_range:["dtype","对象类型不符"],
  mapping_contradiction:["contra","映射矛盾"],
  mapping_contradiction_disjoint:["contra","映射矛盾·disjoint"],
  mapping_unresolved:["unres","一对多未决"],
};

async function refreshPreview(){
  PREVIEW=await api("/api/preview");
  const r=PREVIEW.result;
  $("contentfp").textContent=PREVIEW.contentFingerprint.slice(0,12);
  $("prevrulefp").textContent=PREVIEW.ruleFingerprint.slice(0,12);
  $("previewfp").textContent=PREVIEW.previewFingerprint.slice(0,12);
  $("migcount").textContent=PREVIEW.migrated.length;
  $("dropcount").textContent=r.dropped.length;

  const tb=$("derived").querySelector("tbody");tb.innerHTML="";
  r.derived.forEach(d=>{
    const path=(d.mappingPath||[]).map(st=>{
      let s=`${esc(short(st.source))} <span class="tag ${esc(RELCLASS[st.relation]?RELCLASS[st.relation][0]:"")}">${esc((st.relation||"").split("#")[1]||"drop")}</span>`;
      if(st.target)s+=` → <span class="iri">${esc(short(st.target))}</span>`;
      if(st.branch)s+=` <span class="muted">[分支 ${esc(short(st.branch))}]</span>`;
      return `<div class="mono">${s}</div>`;
    }).join("")||'<span class="muted">无映射</span>';
    let origin="";
    if(d.origin==="deprecated-dropped")origin='<span class="tag closed">废弃丢弃</span>';
    else if(d.origin==="unmapped-dropped")origin='<span class="tag closed">未映射丢弃</span>';
    else if(d.origin==="one-to-many-unresolved")origin='<span class="tag unres">未决保留</span>';
    else origin='<span class="tag" style="color:#a8e6c8;border-color:#2f6b50">已映射</span>';
    tb.insertAdjacentHTML("beforeend",`<tr>
      <td class="mono">${quadStr(d.source)}${d.detail?`<div class="muted">${esc(d.detail)}</div>`:""}</td>
      <td>${origin}${path}</td>
      <td class="mono">${d.derived?quadStr(d.derived):'<span class="muted">— 未进入 v2 —</span>'}</td>
    </tr>`);
  });

  const vb=$("violations").querySelector("tbody");vb.innerHTML="";
  PREVIEW.report.violations.forEach(v=>{
    const [cls,label]=VCAT[v.code]||["dtype",v.code];
    const ev=(v.evidence||[]).map(e=>`<code>${esc(e.quad)}${e.graph?` <span class="muted">[${esc(short(e.graph))}]</span>`:""}${e.note?` <span class="tag unres">${esc(e.note)}</span>`:""}</code>`).join("")||'<span class="muted">无</span>';
    vb.insertAdjacentHTML("beforeend",`<tr>
      <td><span class="tag ${cls}">${esc(label)}</span>${v.mappingBorne?'<span class="tag contra">由映射产生</span>':""}</td>
      <td class="term">${term({kind:"iri",value:focusOf(v)})}</td>
      <td class="mono">${v.path?esc(short(v.path)):"—"}${v.value?" = "+term(v.value):""}</td>
      <td>${esc(v.message)}</td>
      <td class="path">${ev}</td>
    </tr>`);
  });
}
function focusOf(v){return v.focusNode;}

$("btnSuggest").onclick=async()=>{
  try{const d=await post("/api/suggest",{});await loadState();
    toast(d.affected&&d.affected.length?`已列出 ${d.affected.length} 条受影响决议供复议`:"新一轮建议未改变已接受决议");}
  catch(e){toast("建议失败:"+e.message,true);}
};
$("btnPreview").onclick=()=>refreshPreview().then(()=>toast("预览已刷新")).catch(e=>toast(e.message,true));
$("btnPublish").onclick=async()=>{
  try{
    const fp=PREVIEW?PREVIEW.previewFingerprint:"";
    const d=await post("/api/publish",{previewFingerprint:fp});
    toast(`发布成功（#${d.id}，图记录 ${d.before}→${d.after}）`);
    await loadState();
  }catch(e){
    const d=e.data||{};
    if(d.failed){toast(`发布失败已回滚（图记录保持 ${d.after}）：${d.error}`,true);}
    else if(e.status===409){toast("预览指纹已过期，请重新生成预览",true);await refreshPreview();}
    else toast("发布失败:"+e.message,true);
  }
};

loadState().then(loadOntology).then(loadInstances).then(refreshPreview).catch(e=>toast(e.message,true));
