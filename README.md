# Pair-wise GSB — RDF 词汇迁移审阅服务

一个**无外部 triple store** 的自包含 Go 服务：装载两版版本化 RDF 词汇（v1 / v2）、
明示映射候选和小型实例图，在网页上审阅类/属性/个体的对应关系，预览迁移结果与
SHACL 违约，最后**原子地**发布迁移图。数据以简化 Turtle / JSON-LD fixture 形式
内嵌在二进制中。

URI、blank node、language tag、datatype 和 named graph 来源都作为一等结构保留，
绝不被压成普通字符串或“二列表”。

## 安装与运行

```bash
go mod download
go test ./... -count=1
go run ./cmd/server --listen 127.0.0.1:5350
```

打开 <http://127.0.0.1:5350>。

- 默认 SQLite 数据库：`data/gsbmig.db`（可用 `--db` 覆盖）。
- 纯 Go 驱动 `modernc.org/sqlite`，**不需要 CGO**。
- 首次启动内嵌 fixture 并按内容指纹登记导入；之后从 SQLite 恢复映射决议。

## 页面能做什么

1. **本体子图**：切换查看 v1 / v2 的类、属性、`rdfs:subClassOf`、domain/range、
   `owl:deprecated`、多语言 `rdfs:label`（带 `@language`）。
2. **受影响实例**：按主体展开 v1 实例的每条 quad 与其 named graph。
3. **映射候选与决议**：每条候选带
   - 源术语 / 目标术语、术语类型（类/属性）、`源版本→目标版本`；
   - 关系：`exact` / `broader` / `narrower` / `oneToMany` / `deprecated`；
   - 候选**证据**（依据、细节、来源图、证据路径）；
   - 决议状态、决议理由与**生效版本**。
   - 可接受映射、或把旧术语标记为**已废弃且无替代**。
4. **重新自动建议**：基于相同 local name 与同语言标签生成建议；
   **不会覆盖**任何已接受映射，而是把受影响的决议单独列出供复议。
5. **迁移预览**：每条派生 triple 都显示
   `原 triple（含来源图） → 映射路径（关系/分支） → v2 triple`。
6. **SHACL 诊断**：区分四类问题，并给出证据路径：
   - 数据缺失（`minCount`）；
   - datatype 不符（如 `"forty-two"^^xsd:string` 对 `xsd:integer`）；
   - 闭集问题：多出的属性（`sh:closed`）与多出的枚举值（`sh:in`）；
   - 映射本身产生的矛盾：`owl:disjointWith` 同主体双类型、对象 `sh:class`
     不符，以及一对多判别无法定案的“未决”。

## 关键语义与设计

### 图规范化与导出指纹

Blank node 的内部 id 在不同次导入中不稳定，因此**指纹不使用导入时的临时 id**。
`internal/rdf/canonical.go` 对每个 named graph 独立规范化：

1. 为每个 blank node 计算其与命名节点接触边的地面签名；
2. 在 blank-node 邻接上做 Weisfeiler–Lehman 颜色细化，按**分区成员集合**判收敛
   （而不是会每轮重排的颜色编号）；
3. 连通分量内仍对称的小组件（≤7 个 bnode，最多 7! = 5040 种排列）做有界全排列，
   取字典序最小的 N-Quads 渲染；更大组件回退到颜色序。

规范化行按 named graph 分块（`# GRAPH <iri>`）后做 SHA-256，得到同时绑定**图内容
与来源图**的指纹。同构图即使 blank id 或语句顺序不同也得到相同指纹。

### 映射关系

- `exact / broader / narrower`：把源类/属性重写到目标；关系记录在证据路径中。
- `deprecated`：源三元组被丢弃并记录为“废弃丢弃”，不生成任何 v2 triple。
- `oneToMany`：每个分支带一个**条件合取**（属性 `present` / `absent`）。迁移时对
  实例求值：
  - 恰有一个分支满足 → 选择该分支并在路径中标注；
  - 0 个或多个分支满足 → **保留为未决（UNRESOLVED），绝不随机挑一个**，
    实例不获得目标类型，SHACL 面板给出 `mapping_unresolved` 诊断。
- 没有任何映射的属性走“未映射丢弃”，同样保留来源 triple 以便审计。

### 证据路径

- 候选证据：`map:evidence` 中记录依据、细节、来源 named graph 与路径字符串。
- 派生证据：每条迁移 triple 反向链接到其源 quad 与逐步 `mappingPath`。
- SHACL 证据：每条诊断列出触发它的具体 quad（含 graph），若该 quad 由映射产生，
  还会附带 `derived from: <源 quad>`，使“数据本身坏”与“映射造成坏”可区分
  （`mappingBorne` 标记）。

### 指纹去重与失败原子性

- **导入**：按 `(kind, graph, 内容指纹)` 去重登记。
- **预览**：`预览指纹 = SHA256(实例内容指纹 | 规则指纹 | 目标图)`；相同内容+规则
  重复预览命中同一行，不重复落库。
- **规则指纹 / 版本**：只有**有效映射集合**（接受/废弃决议）变化时版本才 +1；
  纯重新建议不会升版本。基于过期版本提交决议返回 `409` 与**冲突子图**
  （同一源/目标术语的全部候选与当前决议）。
- **发布**：在**单个 SQLite 事务**内写入发布记录；提交前钩子失败或任何错误都会
  回滚，**绝不留下部分已迁移图**。相同 `内容指纹+规则指纹` 的发布幂等复用同一条
  记录。测试通过注入一个总是失败的 pre-commit 钩子验证回滚后发布计数不变。

## 代码结构

| 路径 | 职责 |
| --- | --- |
| `cmd/server` | HTTP 服务入口、信号与优雅关停 |
| `internal/rdf` | Term/Quad/Dataset、简化 Turtle 与 JSON-LD 解析、图规范化指纹 |
| `internal/mapping` | 候选解析、自动建议、决议、版本与有效映射规则集 |
| `internal/migrate` | 两阶段迁移（先定类再映射属性）、一对多判别、派生溯源 |
| `internal/shacl` | 简化 SHACL 解析与四类违约校验、证据路径 |
| `internal/store` | SQLite schema、导入/预览/发布持久化、事务化发布 |
| `internal/app` | 编排：引导、预览、建议、决议冲突、原子发布 |
| `internal/web` | 单页审阅 UI 与 JSON API |
| `internal/fixtures`、`fixtures` | 内嵌 Turtle / JSON-LD fixture（两份目录内容一致） |

## 演示数据里覆盖的情形

- `dave`：缺 `firstName`（数据缺失）；
- `bob`：`age` 是字符串（datatype 不符）；
- `erin`：多出未映射的 `contactEmail`（闭集多出属性）；
- `mallory`：同时是 Person 与 Organization，迁移后撞上 `Human owl:disjointWith
  Organization`（映射矛盾），且 `status="archived"` 不在闭集；
- `nina`：直接以 v2 编写、`status="suspended"` 不在闭集（既有数据问题）；
- `frank` 的三个地址：有 country code → `PostalAddress`；只有 full text →
  `Location`；两者皆无 → 保持**未决**；
- `legacyCode` 等 `owl:deprecated` 属性走“废弃丢弃”。
