# Pair-wise GSB — RDF 词汇迁移审阅服务

一个自包含的 Go 服务，用于在两份**版本化 RDF 词汇表**（旧 v1 / 新 v2）之间审查
类、属性与个体的对应关系，并在发布前预览迁移后的图与 SHACL 违约。不连接任何外部
triple store；所有输入都是内置的简化 Turtle/TriG 与 JSON-LD fixture。

URI、blank node、language tag、datatype 与 named graph 来源在内部都作为**带类型的
RDF term**（`internal/rdf/term.go`）保存，绝不压成普通字符串或一张丢语义的二列表。

## 运行

```bash
go mod download
go test ./... -count=1
go run ./cmd/server --listen 127.0.0.1:5350
# 打开 http://127.0.0.1:5350
```

首次启动会把 `fixtures/` 下的演示数据按内容指纹导入内置 SQLite（`gsb.db`）。
再次启动若已存在则不重复导入。可用 `--db /path/to.db`、`--auto-seed=false` 调整。

## 页面能做什么

- **本体子图**：并排展示 v1/v2 的类、属性、个体，含 label（保留 `@lang`）、
  `subClassOf`、`disjointWith`、domain/range、`owl:deprecated` 与定义证据。
- **映射候选 / 决议**：接受 `exact`、`broader`、`narrower`、**一对多**，或把旧术语
  标记为**废弃（无替代）**。每条决议绑定候选证据、生效映射版本和理由。
- **受影响实例**：按个体展示原始 triple，并保留各自的 named graph 来源。
- **迁移预览**：每条派生 triple 都带一条**证据步骤**（原 triple → 映射规则 →
  派生 triple）；未映射、废弃、一对多条件未满足都显式保留，绝不静默丢弃或随机挑目标。
- **SHACL 诊断**：每条违约带类别与证据路径（违约 triple → 迁移步骤 → 原 triple）。

## 映射关系（`internal/mapping`）

| 关系 | 含义 | 目标数 |
| --- | --- | --- |
| `exact` | 等价替换 | 恰好 1 |
| `broader` / `narrower` | 目标概念更宽 / 更窄 | ≥1 |
| `one_to_many` | 一对多，按**判别条件**选目标 | 每个分支一个目标 |
| `deprecated` | 旧术语废弃，**无替代**，triple 被丢弃并留证 | 0 |

一对多规则用 `m:branch` 表达，例如 v1 `Contractor` 依据 `contractType` 迁移到
`StaffMember`（`"FTE"`）或 `ExternalWorker`（`"FIXED"`）。当实例不满足任何分支
（如 `"INTERN"`，或缺字段）时，预览输出 `pending_condition` 步骤而**不是随机选择**。

### 非破坏性重新建议

`POST /api/suggest` 会基于同 IRI、同 local name、同 label 给出新候选，并按**规则
内容指纹去重**。它**绝不覆盖**已接受映射：当新提示与现有决议冲突时，只把受影响的
决议列入 `affected` 供人工复议。

## 证据路径（`internal/migrate`、`internal/shacl`）

每个 `Step` 保存：

- `original`：源 named graph 中的原 triple（含 graph IRI）
- `mapping_id` / `relation` / `source` / `targets` / `condition`
- `derived`：迁移图里的派生 triple
- `kind`：`resolved | unmapped | deprecated | pending_condition`

SHACL 诊断分四类，便于区分「数据本身问题」与「映射引入的矛盾」：

| 类别 | 触发 |
| --- | --- |
| `missing_data` | `sh:minCount` 未满足，且数据本来就缺 |
| `datatype_mismatch` | 字面量 datatype 不合法 / 不符合 `sh:datatype`、nodeKind、`sh:class` |
| `closed_extra_property` | `sh:closed true` 的形状上出现未允许属性 |
| `mapping_contradiction` | 映射本身制造的矛盾：映射进闭集 `sh:in`、映射出不相交类（`owl:disjointWith`）、或废弃映射删掉了形状必需属性 |

fixture 中四类都有对应实例：`bob`（datatype）、`carol`（废弃 staffCode 导致
必需 `personId`/`fullName` 缺失→映射矛盾）、`dept_x`（闭集多出 `ageYears`）、
`alice`/`dave` 等（正常 + 闭集成员），`frank`/`erin`（一对多条件未决）。

## 图规范化与指纹（`internal/rdf/canon.go`）

- blank node 的导入期临时 ID（`_:b1`、`_:j2`）**不参与身份**。
- 每个 named graph 内对 blank node 做**迭代颜色精化（color refinement）**，依据其
  出入边与相邻 term 的结构签名重标为稳定的 `_:c0 … cN`；同构但语句顺序/临时标签
  不同的图得到完全一致的规范化 N-Quads。
- 指纹是规范化行的 SHA-256（取 16 字节十六进制）：导入去重、预览（内容指纹 +
  规则指纹）、发布去重都基于它。
- 字面量按 RDF 1.1 处理：无类型字面量与 `xsd:string` 视为相等；`@lang` 严格区分。

## 持久化与失败原子性（`internal/store`）

使用纯 Go 驱动 `modernc.org/sqlite`（无需 cgo）。

- `documents`：按 `(content_fp, kind)` 唯一，重复导入直接幂等跳过。
- `quads`：以带类型 term 的 N-Triples 形式存主语/谓/宾，另存 named graph IRI。
- `mapping_state`：单调递增的决策 `version` 与版本指纹；`mapping_rules` 及其
  targets / branches / evidence 子表整体在一个事务里重写。
- `previews` / `preview_steps` / `diagnostics`：预览与诊断按内容指纹去重保存。
- `publishes` / `published_quads`：发布在**单个 SQLite 事务**里写状态行和全部派生
  quad。任一步失败即 `ROLLBACK`，**不会留下部分已迁移图**；相同内容+规则指纹重复
  发布被幂等跳过。

并发审阅采用乐观锁：提交决议时携带所基于的 `version`；过期则返回 `409` 与该源
当前已接受决议构成的**冲突子图**，前端展示冲突 quad 与当前版本指纹。

## HTTP API

| 方法路径 | 作用 |
| --- | --- |
| `GET /api/state` | 页面全量状态（本体、映射、实例、预览、已发布图） |
| `POST /api/import` | 导入 Turtle/JSON-LD 文档（内容指纹去重） |
| `POST /api/suggest` | 新一轮自动建议（不覆盖已接受映射） |
| `POST /api/decide` | 提交决议，body 含 `rule_fp`、`version`、`decision`；过期返回 409 + 冲突子图 |
| `POST /api/preview` | 生成迁移预览 + SHACL 诊断 |
| `POST /api/publish` | 单事务原子发布；重复内容幂等 |
| `GET /api/published` | 最新已发布图（含规范化 N-Quads 与指纹） |

## 代码结构

```
cmd/server           启动、参数、首次 fixture 播种
internal/rdf         RDF term、Turtle/TriG 与 JSON-LD 解析、图规范化/指纹
internal/spec        版本化本体（类/属性/个体/不相交/废弃）抽取与证据子图
internal/mapping     候选解析、规则/分支/证据指纹、决议版本、乐观冲突、非破坏建议
internal/migrate     条件化迁移、原 triple↔派生 triple 证据步骤
internal/shacl       简化 SHACL（list、closed、min/maxCount、datatype、in、class、disjoint）与四类诊断
internal/store       SQLite schema、导入去重、映射/预览/诊断、原子发布
internal/service     编排 + 读写锁
internal/web         HTTP API 与内嵌单页审阅界面
fixtures             v1/v2 本体、显式映射候选、实例（JSON-LD + TriG）、v2 SHACL
```

解析器刻意保持「简化但语义忠实」：支持前缀/base、空白节点属性表、RDF collection
（`(a b c)` → 链表）、`@lang`/`^^datatype`、TriG `GRAPH`，以及带 `@context` /
`@graph` / 值对象的 JSON-LD 子集。
