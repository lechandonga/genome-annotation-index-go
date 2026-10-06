# 支持的格式与坐标约定

## 内部统一约定

所有进入索引的特征统一使用：

- **0-based 计数**：序列第一个碱基位置为 `0`；
- **半开区间 `[Start, End)`**：`Start` 包含、`End` 不包含；
- 长度 `Length = End - Start`；
- 重叠判定 `a.Start < b.End && b.Start < a.End`，因此端点相接
  （如 `[0,100)` 与 `[100,200)`）**不算重叠**；
- 提供 FASTA 参考时，要求 `End <= 序列长度`。

换算只经过 `coord.FromRaw` 一个入口，保证混合来源一致：

| 格式 | 源坐标 | 换算 |
| --- | --- | --- |
| GFF3 | 1-based，闭区间 `[start,end]` | `[start-1, end)` |
| GTF | 1-based，闭区间 `[start,end]` | `[start-1, end)` |
| BED | 0-based，半开 `[chromStart,chromEnd)` | 原样 |
| BED12 block | 相对 `chromStart` 的 0-based `blockStart`/`blockSize` | `[chromStart+blockStart, +blockSize)` |

示例：GFF3 行 `1..1`（单碱基）归一为 `[0,1)`；BED 行 `chromStart=99
chromEnd=400` 归一为 `[99,400)`，长度 301。

## GFF3

- 必须以 `##gff-version 3` 开头，否则按 `bad_header` 拒绝。
- 需要恰好 9 个 tab 列；前 8 列任一为空按 `missing_field` 拒绝。
- 链取值 `+ - . ?`（`?` 归一为无链 `.`），非法按 `bad_strand` 拒绝。
- phase 仅允许 `0/1/2/.`，否则 `bad_phase`。
- 属性列为 `key=value`（`;` 分隔）；空键、非 `key=value`、重复键均
  按 `syntax_error` 拒绝。`Parent=a,b` 支持多父级。
- 身份取 `ID` 属性；**无 ID 的记录**不被丢弃，而是生成确定性合成 id：
  `<source>#line<行号>:<type>:<seqid>:<start>:<end>`。
  同一文件重复导入得到相同 id。
- 版本取可选的 `version` 属性。

## GTF（GFF2 方言）

- 9 列；属性为 `key "value";` 形式，未加引号、键重复、缺少属性列均拒绝。
- 身份规则（缺失即拒绝，不静默编造）：
  - `gene`：必须有 `gene_id`，id 即 `gene_id`；
  - `transcript/mRNA/...`：必须有 `gene_id` 与 `transcript_id`，
    父级为 `gene_id`；
  - `exon/CDS`：必须有 `gene_id` 与 `transcript_id`；因为 GTF 常对
    同一 `exon_id` 的多个不连续片段重复使用，实际 id 采用
    `<exon_id>#<seqid>:<start>:<end>`（无 `exon_id` 时用
    `<transcript_id>.exon#...`），保证多行外显子可区分且可复现。

## BED（BED6–BED12）

- 至少 3 列，最多 12 列；按 **tab** 切列（空名列保留为空，不错位）。
- BED6 行成为一个 `transcript` 特征；名称为空时使用与 GFF3 相同规则的
  确定性合成 id。
- BED12 的每个 block 生成一个 `exon`，父级为该行特征；校验：
  - `blockCount >= 1`，sizes/starts 数量必须与之一致；
  - `blockStart >= 0`、`blockSize > 0`；
  - block 必须落在父区间内（越界 = `out_of_range`）；
  - blocks 必须有序且互不重叠（否则 `invalid_block`）。
- `track`/`browser` 行与 `#` 注释跳过（这是格式规定的控制行，不算数据丢弃）。

## FASTA 参考

- header 取 `>` 后第一个空白分隔 token 作为序列 id；
- 重复 id、空 header、数据先于 header、非法碱基字符均为硬错误；
- 解析结果是 `{seqID: 长度}`，导入注释时可选启用：
  - 引用未知序列 → `unknown_reference`；
  - 归一化后 `End > 长度` → `out_of_range`。

## 拒绝原因码

| reason | 含义 |
| --- | --- |
| `syntax_error` | 列数/属性/控制行等语法错误 |
| `missing_field` | 必填列或属性缺失（如 GTF 缺 `gene_id`） |
| `bad_number` | 数值字段不是整数 |
| `bad_coordinate_system` | 坐标小于计数下限、端点倒置 |
| `out_of_range` | 超过参考序列长度或 BED block 越界 |
| `bad_strand` / `bad_phase` / `bad_header` | 链/相位/头部非法 |
| `unknown_reference` | 参考序列中不存在该 seqid |
| `empty_feature` | 归一化后长度为 0 |
| `invalid_block` | BED12 block 数量、顺序或尺寸非法 |
| `unsupported_format` | 无法识别格式或 GFF 版本不是 3 |

拒绝记录包含 `file:line`、reason 与人类可读信息，可按 reason 聚合统计。
