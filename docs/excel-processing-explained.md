# WeKnora Excel 文件处理机制说明

> 调查对象：WeKnora RAG 知识库项目
> 结论要点：**Excel 走纯文本解析路径；开启多模态（VLM）不会优化 Excel 处理。**

---

## 一、Excel 是怎么被解析的

Excel 文件（`.xlsx` / `.xls` / `.ods`）由独立的 **`docreader`（Python）服务**解析，核心代码在：

```
docreader/parser/excel_parser.py  →  class ExcelParser.parse_into_text()
```

### 解析流程（纯文本转换）

1. 用 **pandas** 打开工作簿，遍历 **每个 sheet**（`excel_file.sheet_names`）。
2. 逐行读取，丢掉全空行（`df.dropna(how="all")`）。
3. 每行转成 **`"列名: 值,列名: 值"`** 的键值对文本（`excel_parser.py` 第 83–92 行）。
4. **每一行 = 一个 chunk**（第 97–99 行，`seq` 递增）。

### 举例

输入表格：

| Name  | Age | City |
|-------|-----|------|
| Alice | 30  | NYC  |
| Bob   | 25  | LA   |

解析结果（每行一个 chunk）：

```
Name: Alice,Age: 30,City: NYC
Name: Bob,Age: 25,City: LA
```

### 附带的工程化处理

解析器还做了不少健壮性处理，但**产出始终是纯文本 chunk，不提取任何图片**：

- `.xls` / `.ods` → `.xlsx` 转换（LibreOffice / soffice）
- 损坏的 xlsx 修复（`xlsx_repair.py`，如 `sharedStrings.xml` 缺失）
- **合并单元格填充**（`xlsx_merge.py` 的 `fill_merged_cells_xlsx`）
- openpyxl 引擎选择与列名兜底（`get_column_letter`）

---

## 二、开启多模态是否会优化 Excel 处理

### 明确结论：**不会。**

多模态（VLM）阶段的触发条件是硬性的（`internal/application/service/knowledge_process.go` 第 676 行）：

```go
if options.EnableMultimodel && len(options.StoredImages) > 0 {
    // 走 VLM / OCR / caption
} else {
    skipStage(types.StageMultimodal, "skipped")  // ← Excel 永远走这里
}
```

Excel 解析器返回的 `ImageRefs` / `StoredImages` **恒为空**（`excel_parser.py` 全文没有任何图片提取逻辑）。因此即使把 KB 的多模态开关（`VLMConfig.Enabled`）打开，对 Excel 而言：

```
EnableMultimodel = true   ✅（你开了）
len(StoredImages) = 0     ❌（Excel 没图片）
→ true && false = false   → 多模态阶段被跳过
```

### VLM / 多模态实际作用范围

| 内容类型 | 是否走 VLM | 说明 |
|---|---|---|
| **PDF 内嵌图片**（图表、扫描页） | ✅ | 提取图片 → VLM 做 OCR + caption |
| **原生上传图片**（JPG/PNG） | ✅ | 直接 VLM 分析 |
| **Excel** | ❌ | 解析器不产出图片，多模态分支不触发 |

---

## 三、想优化 Excel 效果，该怎么做

既然多模态帮不上忙，真正能改善 Excel 检索/问答效果的方向（按性价比排序）：

| # | 方向 | 说明 |
|---|---|---|
| ① | **合并单元格 / 表头** | 已有 `xlsx_merge.py` 填充合并单元格。多级表头场景下，可改解析器让表头语义随每行带上（当前 `列名: 值` 已部分做到）。 |
| ② | **每行一 chunk 的粒度** | 对"一行=一条独立记录"（名单、台账）很好；但对"需要横向对比整表"的场景不足——检索只命中单行。可额外生成一个"整表摘要 chunk"。 |
| ③ | **转 Markdown 表格** | 当前是 CSV 式 `列: 值`。若想让 LLM 更好理解结构，可把每行/整表改成 markdown 表格格式。属解析器改造，非开关。 |
| ④ | **提取 Excel 内嵌图表/图片** | 当前内嵌图表会被**完全忽略**。要让 VLM 对 Excel 生效，需改 `excel_parser.py` 提取内嵌图片并写入 `ImageRefs` —— **这才是"让多模态对 Excel 生效"的唯一正确做法（改代码，不是改开关）。** |

---

## 四、关键代码位置对照

| 功能 | 文件路径 | 行号 |
|---|---|---|
| Excel 解析主逻辑 | `docreader/parser/excel_parser.py` | 51–103 |
| 打开工作簿 / 引擎选择 | `docreader/parser/excel_parser.py` | 133–187 |
| 合并单元格填充 | `docreader/parser/xlsx_merge.py` | — |
| 损坏 xlsx 修复 | `docreader/parser/xlsx_repair.py` | — |
| 多模态是否启用判断 | `internal/types/knowledgebase.go` | 664–677 |
| 多模态阶段触发条件 | `internal/application/service/knowledge_process.go` | ~676 |

---

## 五、一句话总结

> **Excel = 纯文本（每行键值对 → 一个 chunk），多模态开关对它无效，因为 Excel 解析器不产出图片。想让 VLM 处理 Excel 里的图表，必须改解析器去提取内嵌图片，而不是打开开关。**
