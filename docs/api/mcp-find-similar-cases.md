# find_similar_cases MCP 工具使用指南

[返回目录](./README.md)

WeKnora 暴露的 MCP 工具之一，用于在 Cases 知识库中查找与指定 case 或问题描述语义相似的历史 support case，辅助排查和解决。

---

## 连接配置

在 Claude Desktop、Cursor 等 MCP 客户端的配置文件中添加：

```json
{
  "WeKnora": {
    "type": "http",
    "url": "http://10.47.202.30:8080/mcp",
    "headers": {
      "X-API-Key": "sk-1IRFIgX0H5pOFedi2az-SUCuwoq9Q_Y0ptU1O8PqpGhZhO36",
      "X-User-Id": "${YOUR_NTID}"
    }
  }
}
```

`X-User-Id` 填写 Qualcomm NTID，不带 `@qualcomm.com`，如 `hanwzhan`。

---

## 输入参数

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `case_number` | string | 二选一 | 源 case 编号，如 `"8642005"`。工具自动查找该 case 的 subject + description 作为搜索 query，并在结果中排除该 case 自身 |
| `query` | string | 二选一 | 自由文本描述，如 `"FBRX calibration failure on SM8650"` |
| `limit` | int | 否 | 返回条数，默认 5，最大 20 |

`case_number` 和 `query` 都传时，`case_number` 优先用于构建搜索 query。

---

## 输出字段

每条结果包含以下字段：

| 字段 | 说明 |
|------|------|
| `case_number` | Case 编号 |
| `subject` | Case 标题 |
| `problem_area_1` | 问题一级分类（如 `BSP/HLOS`） |
| `problem_area_2` | 问题二级分类（如 `BOOT/Bringup`） |
| `problem_area_3` | 问题三级分类（如 `UEFI-XBL`） |
| `chipsets` | 涉及的芯片型号列表（如 `["SA8295P"]`） |
| `case_url` | Salesforce 原始 case 链接 |
| `problem_summary` | 问题描述全文（Case Overview — 客户遇到了什么） |
| `solution_summary` | 解决方案全文（Key Steps to Solve + Proposed Final Solution） |
| `score` | 语义相关性分数 |

返回结构示例：

```json
{
  "results": [
    {
      "case_number": "8554017",
      "subject": "Memory Hole in 12GB DRAM Configuration",
      "problem_area_1": "BSP/HLOS",
      "problem_area_2": "BOOT/Bringup",
      "problem_area_3": "UEFI-XBL",
      "chipsets": ["SA7255P"],
      "case_url": "https://qualcomm-cdmatech-support.my.salesforce.com/500dK00000MNKvqQAH",
      "problem_summary": "### Case Overview\nCustomer integrated two 6GB LPDDR5 chips (12GB total) with QC7255 SOC...",
      "solution_summary": "### Key Steps to Solve Problem\n1. Collected showmem output...\n\n### Proposed Final Solution\nApply DRAM config guide MCN 80-25300-1 Rev AC...",
      "score": 0.0105
    }
  ],
  "total": 5
}
```

---

## 使用场景

### 按 case 编号查相似

```
Has this issue (Case 8642005) happened before?
```

LLM 会调用：
```json
{ "case_number": "8642005", "limit": 5 }
```

### 按症状描述查相似

```
Find similar cases about FBRX calibration failure on SM8650
```

LLM 会调用：
```json
{ "query": "FBRX calibration failure SM8650", "limit": 5 }
```


## 说明

- 只搜索名称为 `Cases` 的知识库，不搜其他 KB，无需传 usid 或知识库 ID
- 结果按 `knowledge_id` 去重，每个 case 只返回一条，不会因多个 chunk 命中同一 case 而重复
- `problem_summary` 固定取 Case Overview chunk，`solution_summary` 固定取 Key Steps / Proposed Final Solution chunk，内容稳定，不是随机命中的 chunk
- 结果按语义相关性排序，`score` 越高越相关
