# 本地基因组注释索引（genome-annotation-index-go）

一套完全本地运行、零外部服务依赖的基因组注释数据接入与区间查询组件：用本地
文件模拟多个来源的参考序列与注释，构建可持久化的查询索引，并对畸形记录、
标识冲突、版本演进、索引重建与崩溃恢复给出确定且可解释的行为。

## 目录结构

| 路径 | 职责 |
| --- | --- |
| `pkg/model` | 统一特征模型、区间语义、稳定的拒绝原因分类 |
| `pkg/parse` | GFF3 / GTF / BED(3/4/6/12) / FASTA 解析与坐标归一 |
| `pkg/registry` | 父子关系重建、标识/类型冲突、版本与回滚、事务校验 |
| `pkg/index` | 不可变区间索引，按区间、链方向、类型查询 |
| `pkg/persist` | 原子快照、SHA-256 校验、完整性自检 |
| `pkg/store` | 并发安全的导入/回滚/重建/查询门面 |
| `cmd/gai` | 本地命令行 |
| `examples/` | 可直接导入的 FASTA + GFF3 示例 |

## 支持的格式与坐标约定

系统内部统一使用 **0 基、半开区间 `[Start, End)`、前向链坐标空间**：

| 格式 | 源坐标约定 | 归一方式 |
| --- | --- | --- |
| GFF3 / GTF | 1 基、闭区间 `[start,end]` | `[start-1, end)` |
| BED 3/4/6/12 | 0 基、半开 `[start,end)` | 原样保留 |
| FASTA | 参考序列 | 记录 `id / length / sha256`，不存原始碱基 |

- 链方向只接受 `+`、`-`、`.`（不分链），坐标始终是前向链基因组坐标，
  链方向不改变坐标数值，只影响方向感知查询与遍历顺序。
- GFF3 无 `ID` 的外显子/CDS：仅当恰好有一个 `Parent` 时接受，并派生
  确定性 ID（`exon:<parent>:<start>-<end>`），否则按 `missing_id` 拒绝。
- GTF 缺少显式 `gene` 行时，根据 `gene_id` 合成基因节点，并由其子代
  转录本/外显子推导跨度。
- BED12 展开为一条转录本 + `blockCount` 条外显子，块越界立即拒绝。

### 非法输入不静默跳过

每条坏记录都会产生一条带 **稳定类别（`RejectKind`）+ 文件 URI + 行号 +
说明**的拒绝原因，类别包括：

`malformed_record`、`missing_field`、`missing_id`、`bad_coordinate`、
`bad_strand`、`bad_attribute`、`coordinate_out_of_bounds`、
`duplicate_id`、`dangling_parent`、`circular_reference`、`type_conflict`、
`sequence_conflict`、`version_regression`、`snapshot_error`、
`source_error`。

默认要求先导入 FASTA：特征引用未知序列或区间超过序列长度都按
`coordinate_out_of_bounds` 拒绝（可用 `--lenient-seq` / `LenientSequences`
 显式放宽，但不建议）。

## 层级、版本与冲突规则

- 允许的父子层级为 `gene → transcript → exon/CDS`；`other` 类型可挂在
  任意节点下但不能成为层级特征的父节点，违反时报 `type_conflict`。
- `Parent` 指向不存在的节点报 `dangling_parent`；成环报
  `circular_reference`（错误信息给出环路径）。
- 同一导入批次内重复 ID 报 `duplicate_id`（第二条开始逐条报告，并指向首次
  出现行）。同一 ID 跨版本改变类型报 `type_conflict`。
- 每个 ID 保留完整版本历史；重复导入**内容完全相同**的记录计为幂等
  no-op，不产生新版本；内容变化时版本自动 +1。GFF3 可用 `version` 属性
  显式给版本号，低于/等于当前版本且内容又变化时报 `version_regression`。
- 回滚不是“删掉新版本”，而是把指定历史版本的**内容**追加为最新版本：
  查询结论与该历史版本完全一致，同时版本号仍然单调递增，操作可重放。
- 每次导入是**事务**：解析期或校验期出现任何拒绝，整个批次放弃，视图与
  磁盘快照都保持原样（不会写入 g2 却丢掉 g3）。

## 查询语义

- 区间按半开重叠判定：`feature.Start < q.End && q.Start < feature.End`，
  因此首尾相接（如 `[0,10)` 与 `[10,20)`）不算重叠。
- `--strand +` / `-` 过滤链方向；反链结果按起点降序返回，对应 5′→3′ 遍历。
- 输出顺序确定（同链：起点、终点、ID 升序），相同输入下结论可复现。

## 持久化、重建与恢复语义

索引目录包含：

```
snapshot.json         规范化 JSON 快照（特征、版本历史、序列、操作日志）
snapshot.json.sha256  快照内容的 SHA-256
```

- **原子写**：先写同目录 `*.tmp` 并 fsync，再 `rename` 覆盖；崩溃后要么是
  旧完整快照，要么是新完整快照，不会出现半截/混合文件。
- **启动校验**：打开时重算 SHA-256 并做结构自检（版本单调、当前版本
  指针有效、无悬空父引用、序列都存在等）。校验失败立即返回
  `snapshot_error`，**绝不返回可能错误的查询结果**。
- **重建（rebuild）**：根据快照内记录的操作日志（每个导入文件的 URI、
  格式、来源）从本地文件按原顺序重放；回滚操作同样重放，所以重建结果与
  演进过程得到的最终视图一致。源文件缺失或已变为含坏记录时，重建显式
  失败（`source_error`），而不是返回部分数据。
- 快照完全损坏/丢失时，可用 `rebuild --from a.fa,b.gff3,...` 或
  `store.RebuildFromSources` 从本地来源安全重建。
- **并发可见性**：查询持有的是不可变 `(snapshot, index)` 视图；重建/导入
  在旁边构建完整新视图，完成后用一次指针切换发布。读者只能看到重建前或
  重建后的完整视图，不会出现新旧混合、重复或遗漏（由 `-race` 并发测试
  持续校验）。

## 本地验证

要求 Go 1.25+（在 1.26 上验证），无需任何外部服务：

```bash
# 全量测试（含竞态检测需要 cgo；无 cgo 时去掉 -race）
go test ./...
CGO_ENABLED=1 go test -race ./...

# 查看“覆盖了哪些业务、各业务下用例是否通过”的分组日志
go test -v ./...
# 或使用封装脚本（go vet + gofmt 检查 + 全量测试）
./scripts/verify.sh
```

测试按业务分组打印，例如：

```
BUSINESS [parse.gff3] (8 cases)
  [PASS] 1-based closed -> 0-based half-open conversion
  ...
BUSINESS [store.concurrent_rebuild] (1 cases)
  [PASS] readers see only complete pre/post views during import+rebuild
```

CLI 冒烟：

```bash
go run ./cmd/gai import --index .gai-index --file examples/chr1.fa --source ref
go run ./cmd/gai import --index .gai-index --file examples/genes.gff3 --source annot
go run ./cmd/gai query  --index .gai-index --seq chr1 --start 0 --end 100
go run ./cmd/gai query  --index .gai-index --seq chr1 --start 0 --end 200 --strand -
go run ./cmd/gai children --index .gai-index --id GENE1
go run ./cmd/gai rollback --index .gai-index --id GENE1 --version 1
go run ./cmd/gai rebuild --index .gai-index
go run ./cmd/gai info --index .gai-index
```

## 测试覆盖的业务

- 畸形输入：列数错误、非数字/零/负坐标、非法链、坏属性、缺失 ID、
  FASTA 坏碱基、BED 块越界等，逐条给出区分性拒绝原因。
- 坐标与链方向边界：1 基→0 基换算、半开相接不算重叠、点查询为空、
  正负链过滤与反链降序、类型过滤。
- 标识冲突与版本演进：批内重复、悬空/环形引用、层级类型冲突、序列冲突、
  自动/显式版本、版本回退拒绝、回滚后版本单调且结论可复现、重启一致。
- 重建与恢复：截断/篡改快照失败闭合、临时文件不被当作快照、从来源重建
  结果一致、源文件缺失显式失败、重建期间 16 个并发读者只观察到完整视图。
