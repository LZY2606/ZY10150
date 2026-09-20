let state = null;
let currentRule = null;

const $ = (id) => document.getElementById(id);
const esc = (s) => String(s ?? "").replace(/[&<>"]/g, (c) =>
  ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" }[c]));
const shortIRI = (u) => {
  if (!u) return "";
  return u.replace(/^.*[#/]/, (m) => m.includes("#") ? "v:" : "");
};
const labelOf = (iri, oldO, newO) => {
  const t = (oldO?.terms || {})[iri] || (newO?.terms || {})[iri];
  const en = (t?.labels || []).find((l) => !l.lang || l.lang === "en");
  return en ? en.value : iri;
};

async function api(path, opts = {}) {
  const res = await fetch(path, {
    headers: { "Content-Type": "application/json" },
    ...opts,
  });
  const body = await res.json().catch(() => ({}));
  if (!res.ok) throw { status: res.status, body };
  return body;
}

function banner(msg, cls = "") {
  const b = $("banner");
  b.textContent = msg;
  b.className = "banner " + cls;
  setTimeout(() => b.classList.add("hidden"), 5000);
}

async function load() {
  state = await api("/api/state");
  render();
}

function render() {
  $("version").textContent = state.mapping.version;
  $("versionfp").textContent = (state.mapping.version_fp || "").slice(0, 16);
  renderTerms("oldTerms", state.old);
  renderTerms("newTerms", state.new);
  renderRules();
  renderInstances();
  renderPublished();
}

function renderTerms(id, o) {
  const el = $(id);
  if (!o) { el.innerHTML = '<div class="ev">尚未导入。</div>'; return; }
  el.innerHTML = o.order.map((iri) => {
    const t = o.terms[iri];
    const labels = t.labels.map((l) =>
      `<span class="badge">${esc(l.value)}${l.lang ? "@" + esc(l.lang) : ""}</span>`).join("");
    const extra = [
      t.parents.length ? `<div class="ev">subClassOf: ${t.parents.map(shortIRI).join(", ")}</div>` : "",
      t.deprecated ? `<span class="badge deprecated">deprecated</span>` : "",
      t.disjoint.length ? `<div class="ev">disjointWith: ${t.disjoint.map(shortIRI).join(", ")}</div>` : "",
      t.domain.length ? `<div class="ev">domain: ${t.domain.map(shortIRI).join(", ")}</div>` : "",
      t.range.length ? `<div class="ev">range: ${t.range.map(shortIRI).join(", ")}</div>` : "",
    ].join("");
    return `<div class="term">
      <div class="iri">${esc(iri)}</div>
      <div>${labels}<span class="badge">${esc(t.kind)}</span></div>${extra}
    </div>`;
  }).join("");
}

function renderRules() {
  const tb = document.querySelector("#rulesTable tbody");
  // candidates + accepted in one table for review
  const rules = state.mapping.rules.slice().sort((a, b) =>
    a.source.localeCompare(b.source) || a.rule_fingerprint.localeCompare(b.rule_fingerprint));
  tb.innerHTML = rules.map((r) => {
    const targets = (r.branches && r.branches.length)
      ? r.branches.map((b) =>
          `<span class="branch">→ ${esc(shortIRI(b.target))} 当 ${esc(b.condition.predicate.replace(/[#/][^#/]*$/, ""))} ${esc(b.condition.value_iri ? "<" + b.condition.value + ">" : '"' + b.condition.value + '"')}</span>`
        ).join("")
      : (r.targets || []).map((t) => `<div>${esc(shortIRI(t))}</div>`).join("");
    const ev = (r.evidence || []).map((e) =>
      `<div class="ev">[${esc(e.type)}] ${esc(e.detail)}${e.quad ? "<br><code>" + esc(e.quad) + "</code>" : ""}</div>`
    ).join("");
    const stLabel = r.status === "deprecated"
      ? "已废弃"
      : r.status === "accepted" ? "已接受 v" + r.accepted_version
      : r.status === "superseded" ? "已被新决议取代"
      : "候选";
    const deprecated = r.relation === "deprecated";
    return `<tr>
      <td class="mono">${esc(shortIRI(r.source))}<br><span class="ev">${esc(r.source)}</span></td>
      <td>${esc(r.term_kind)}</td>
      <td class="rel ${esc(r.relation)}">${esc(r.relation)}${deprecated ? "（无替代）" : ""}</td>
      <td>${targets || '<span class="ev">—</span>'}</td>
      <td>${ev}</td>
      <td class="status-${esc(r.status)}">${stLabel}${r.rationale ? '<div class="ev">' + esc(r.rationale) + "</div>" : ""}</td>
      <td><button data-fp="${esc(r.rule_fingerprint)}" class="decide">决议</button></td>
    </tr>`;
  }).join("");
  tb.querySelectorAll(".decide").forEach((btn) =>
    btn.addEventListener("click", () => openDialog(btn.dataset.fp)));
}

function openDialog(fp) {
  const r = state.mapping.rules.find((x) => x.rule_fingerprint === fp);
  if (!r) return;
  currentRule = r;
  $("dRelation").value = r.relation;
  $("dTargets").value = (r.targets || []).join("\n");
  $("dRationale").value = r.rationale || "";
  $("decideDialog").showModal();
}

$("decideForm").addEventListener("submit", async (e) => {
  if (e.submitter && e.submitter.value === "cancel") return;
  e.preventDefault();
  const targets = $("dTargets").value.split("\n").map((s) => s.trim()).filter(Boolean);
  // One-to-many uses existing branches when targets not hand-edited.
  const r = currentRule;
  const payload = {
    rule_fp: r.rule_fingerprint,
    version: state.mapping.version,
    decision: {
      term_kind: r.term_kind,
      relation: $("dRelation").value,
      source: r.source,
      targets: $("dRelation").value === "one_to_many" && r.branches.length
        ? [] : targets,
      branches: $("dRelation").value === "one_to_many" && r.branches.length
        ? r.branches
        : ($("dRelation").value === "one_to_many"
            ? targets.map((t) => ({ target: t, condition: { predicate: "", value: "" } }))
            : []),
      rationale: $("dRationale").value,
      evidence: r.evidence,
    },
  };
  try {
    const out = await api("/api/decide", { method: "POST", body: JSON.stringify(payload) });
    banner(`决议已生效，映射版本 ${out.version}`, "ok");
    await load();
    $("decideDialog").close();
  } catch (err) {
    if (err.status === 409) {
      const c = err.body.conflict;
      banner(`冲突：你的审阅基于过期版本 ${payload.version}（当前 ${err.body.current_version}）。` +
        `冲突决议：${c.rule.source} → ${(c.rule.targets || []).join(", ")}（${c.rule.status}）`, "err");
      showConflict(c);
    } else {
      banner("决议失败：" + (err.body.error || err), "err");
    }
  }
});

function showConflict(c) {
  const el = $("affected");
  el.classList.remove("hidden");
  el.innerHTML = `<b>并发冲突子图</b>
    <div class="mono">${(c.conflict_subgraph || []).map(esc).join("<br>")}</div>
    <div class="ev">当前版本指纹 ${esc((c.current_fp || "").slice(0, 16))}；请刷新后在最新决议上复议。</div>`;
}

function renderInstances() {
  const el = $("instances");
  if (!state.instances || !state.instances.length) {
    el.innerHTML = '<div class="ev">尚未导入实例图。</div>';
    return;
  }
  el.innerHTML = state.instances.map((i) => `
    <div class="inst">
      <div class="mono">${esc(shortIRI(i.subject))}</div>
      <div>${(i.types || []).map((t) => `<span class="badge">${esc(shortIRI(t))}</span>`).join("")}</div>
      <div class="g">graph: ${esc(i.graph || "(default)")}</div>
      <div class="ev mono" style="margin-top:6px">${i.quads.map(esc).join("<br>")}</div>
    </div>`).join("");
}

function renderPreview(pv) {
  if (!pv) { $("previewMeta").textContent = "尚未生成。"; $("steps").innerHTML = ""; return; }
  const r = pv.report;
  $("previewMeta").innerHTML =
    `映射版本 ${r.mapping_version}（规则指纹 <code>${esc((r.rule_fp || "").slice(0,16))}</code>） ·
     源图指纹 <code>${esc(r.source_fp.slice(0,16))}</code> ·
     迁移图指纹 <code>${esc(r.content_fp.slice(0,16))}</code> ·
     步骤 ${pv.step_count}（条件未决 ${pv.pending_count}）`;
  $("steps").innerHTML = r.steps.map((st) => `
    <div class="step ${esc(st.kind)}">
      <div><b>${esc(st.kind)}</b> ${st.mapping_id ? "via " + esc(st.mapping_id) : ""}
        ${st.relation ? '<span class="rel ' + esc(st.relation) + '">(' + esc(st.relation) + ")</span>" : ""}</div>
      <div class="mono">原: ${esc(quadText(st.original))}</div>
      ${st.derived ? `<div class="mono">派生: ${esc(quadText(st.derived))} @ ${esc(r.migrated_graph)}</div>` : ""}
      ${st.condition ? `<div class="branch">条件: ${esc(st.condition)}</div>` : ""}
      <div class="why">${esc(st.explanation)}</div>
    </div>`).join("");
  const cats = pv.categories || {};
  const labels = {
    missing_data: "数据缺失", datatype_mismatch: "datatype 不符",
    closed_extra_property: "闭集属性多出", mapping_contradiction: "映射产生的矛盾",
  };
  $("diagCats").innerHTML = Object.entries(cats).map(([k, n]) =>
    `<span class="cat ${k}">${labels[k] || k}: ${n}</span>`).join("");
  document.querySelector("#diagTable tbody").innerHTML = pv.diagnostics.map((v) => `
    <tr>
      <td class="cat ${esc(v.category)}" style="border:0">${esc(labels[v.category] || v.category)}</td>
      <td class="mono">${esc(shortIRI(v.node))}</td>
      <td class="mono">${esc(shortIRI(v.path))}</td>
      <td class="mono">${esc(v.value)}</td>
      <td>${esc(v.message)}${v.mapping_cause ? '<div class="ev">映射 ' + esc(v.mapping_cause) + "</div>" : ""}</td>
      <td class="ev mono">${(v.evidence || []).map(esc).join("<br>")}</td>
    </tr>`).join("");
}

function quadText(q) {
  return `${q.s ? term(q.s) : term(q.Subject)} ${q.p ? term(q.p) : term(q.Predicate)} ${q.o ? term(q.o) : term(q.Object)}`;
}
function term(t) {
  if (!t) return "";
  if (t.kind === 0) return `<${t.value}>`;
  if (t.kind === 1) return `_:${t.value}`;
  if (t.kind === 2) {
    if (t.lang) return `"${t.value}"@${t.lang}`;
    if (t.datatype && !t.datatype.endsWith("#string")) return `"${t.value}"^^<${t.datatype}>`;
    return `"${t.value}"`;
  }
  return t.value || "";
}

function renderPublished() {
  const el = $("published");
  const p = state.published;
  if (!p) { el.innerHTML = '<div class="ev">尚未发布。发布在单事务内完成，失败不留部分图。</div>'; return; }
  el.innerHTML = `<div class="ev">指纹 <code>${esc(p.fingerprint)}</code> · ${p.canonical.length} 条规范化 N-Quads</div>
    <details><summary>查看规范化导出（blank node 基于结构重标 cN）</summary>
    <pre class="mono">${esc(p.canonical.join("\n"))}</pre></details>`;
}

$("refresh").addEventListener("click", () => load().then(() => banner("已刷新", "ok")));
$("suggest").addEventListener("click", async () => {
  try {
    const rep = await api("/api/suggest", { method: "POST" });
    const aff = (rep.affected || []).map((r) =>
      `<div class="mono">${esc(shortIRI(r.source))} 已接受 ${esc(r.relation)} → ${(r.targets||[]).map(shortIRI).join(", ")}</div>`).join("");
    if (aff) $("affected").classList.remove("hidden"),
      ($("affected").innerHTML = "<b>新建议影响的已有决议（不会被覆盖，请复议）</b>" + aff);
    banner(`新建议：新增 ${rep.added.length}，重复 ${rep.duplicated.length}，影响决议 ${rep.affected.length}`, "ok");
    await load();
  } catch (err) { banner(err.body?.error || String(err), "err"); }
});
$("preview").addEventListener("click", async () => {
  try {
    const pv = await api("/api/preview", { method: "POST" });
    renderPreview(pv);
    banner("预览已生成（内容+规则指纹去重）", "ok");
    await load(); renderPreview(pv);
  } catch (err) { banner(err.body?.error || String(err), "err"); }
});
$("publish").addEventListener("click", async () => {
  try {
    const out = await api("/api/publish", { method: "POST" });
    banner(out.inserted ? `已发布 #${out.publish_id}` : "相同内容指纹已发布，幂等跳过", "ok");
    await load();
  } catch (err) { banner("发布失败（已回滚，无部分图）：" + (err.body?.error || err), "err"); }
});

load().catch((e) => banner("加载失败：" + e, "err"));
