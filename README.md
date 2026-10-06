# 本地基因组注释接入与区间查询组件（Go）

一个零外部依赖、可本地复现的基因组注释数据接入与区间查询组件。它用本地
文件模拟多来源注释与参考序列，构建可持久化的查询索引，并对畸形记录、
标识冲突、版本演进、导入/重建期间的并发查询给出确定、可解释的结论。

## 能力总览

- 解析 **GFF3 / GTF / BED（BED6–BED12）** 注释与 **FASTA** 参考序列，
  归一到统一特征模型（`model.Feature`）。
- 坐标体系统一换算为 **0-based、半开区间 `[start,end)`**；GFF3/GTF 的
  1-based 闭区间按 `[start-1, end)` 转换，BED 保持 0-based 半开。
  详见 [`docs/coordinates.md`](docs/coordinates.md)。
- 字段缺失、格式非法、坐标越界、未知参考序列等**逐条拒绝**，返回带
  reason code 的结构化原因（`parse.Reject`），绝不静默跳过。
- 重建 **基因 → 转录本 → 外显子/CDS** 父子关系；重复 id、悬空父级、
  自环/环形引用、非法父子类型、版本回退全部显式报错；每个批次原子提交。
  详见 [`docs/versioning.md`](docs/versioning.md)。
- 支持区间重叠查询、链方向过滤（含/不含无链特征、严格链）、类型过滤、
  祖先链扩展；结果按 `(start,end,id)` 确定排序。
- 持久化采用**只追加、带 SHA-256 校验的事件日志** + 原子替换的快照/清单；
  进程重启后结论一致，重建期间并发查询只能看到重建前或重建后的完整视图。
  详见 [`docs/recovery.md`](docs/recovery.md)。

## 目录结构

| 路径 | 职责 |
| --- | --- |
| `coord/` | 坐标体系定义与 1-based/0-based 换算、边界校验 |
| `model/` | 统一特征模型（类型、链、来源、版本） |
| `parse/` | GFF3/GTF/BED/FASTA 解析、格式嗅探、拒绝原因 |
| `graph/` | 父子关系重建、版本管理、冲突与环检测、不可变快照 |
| `store/` | 事件日志持久化、原子提交/回滚/重建、区间索引、崩溃恢复 |
| `importer/` | 解析 → 校验 → 原子提交 的端到端编排 |
| `cmd/gaidx/` | 本地命令行（import/query/rollback/rebuild/status） |
| `testdata/` | 本地可复现的多格式样例文件 |

## 快速开始

要求 Go 1.25+（在 1.26 上验证）。

```bash
go build ./...

# 导入多个来源（可选 FASTA 做坐标越界校验）
go run ./cmd/gaidx import --dir ./.gaidx --fasta testdata/ref.fa \
    testdata/demo.gff3

# 0-based 半开区间查询：[0,120)，仅正链，限定类型
go run ./cmd/gaidx query --dir ./.gaidx --seq chr1 --start 0 --end 120 \
    --strand + --types gene,transcript

go run ./cmd/gaidx status   --dir ./.gaidx
go run ./cmd/gaidx rebuild  --dir ./.gaidx
go run ./cmd/gaidx rollback --dir ./.gaidx --to 0
```

畸形文件会逐行给出可区分原因，且不会写入任何数据：

```bash
go run ./cmd/gaidx import --dir ./.gaidx testdata/malformed.gff3
# parsed=2 rejected=6
#   reject bad_coordinate_system: 2
#   reject bad_number: 1
#   reject bad_strand: 1
#   reject syntax_error: 2
```

## 本地验证

```bash
# 全部测试（-v 日志按“业务: ……”分组，展示每个业务的用例与结果）
go test ./... -v -count=1

# 只跑持久化/并发/恢复相关
go test ./store/... -v -count=1

# 竞态检测（需要 cgo 的环境使用）
CGO_ENABLED=1 go test -race ./...
```

测试覆盖的业务域：

- 坐标归一与半开边界（`coord`、`parse`、`store`）；
- GFF3/GTF/BED 畸形输入与差异化拒绝原因；
- FASTA 重复 id、非法碱基、未知参考序列、坐标越界；
- 重复标识、版本演进/回退/重复版本、悬空与环形引用、非法父子类型；
- 跨进程持久化与重启复现、回滚后再提交；
- 截断事件尾恢复、校验和损坏明确失败、快照缺失自动重建；
- 重建/提交与并发查询的完整视图一致性（数万个并发采样）。

## 作为库使用

```go
st, _ := store.Open("./.gaidx", store.OpenOptions{CopySources: true})
defer st.Close()

rep, err := importer.Import(st,
    []importer.File{{Path: "a.gff3", SourceName: "ensembl"}},
    "ref.fa", graph.ConflictReject)

refs := st.Query(store.Query{
    SeqID:  "chr1",
    Region: coord.Interval{Start: 0, End: 120},
    Strand: store.StrandForwardOnly,
    Types:  []model.FeatureType{model.Gene, model.Transcript},
})
```

## 非目标

- 不连接任何数据库、网络服务或远程注册表；
- 不做序列比对/注释推断，只做解析、归一、关系重建与区间检索；
- 索引规模面向本地工作集（读时快照 + 每序列有序切片）。
