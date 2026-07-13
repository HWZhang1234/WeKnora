# WeKnora Excel and OCR Processing Analysis

## Executive Summary

This analysis covers how WeKnora processes Excel files and PDFs with OCR routing.

### Key Points:
- **Excel**: Uses openpyxl, pandas, xlrd for native parsing with merged cell handling
- **OCR**: Does NOT run OCR in docreader; classifies PDF pages and routes to Go backend
- **Architecture**: Separation of concerns - Python handles parsing, Go handles ML/OCR

---

## Excel File Processing

### Libraries
- openpyxl >= 3.1.0 (XLSX)
- pandas >= 2.0.0 (DataFrames)  
- xlrd >= 2.0.0 (Legacy XLS)
- markitdown (Microsoft converter)

### Key Components

**ExcelParser** (docreader/parser/excel_parser.py):
- Input: raw bytes
- Output: Document with markdown content + row-based chunks
- Features: multi-sheet, merged cells, format detection

**Special Handling**:
1. Merged cells (xlsx_merge.py) - fills all cells with master value
2. XLSX repair (xlsx_repair.py) - fixes phantom sharedStrings
3. Format conversion (excel_convert.py) - LibreOffice bridge for legacy formats
4. Parser registry (registry.py) - engine selection with fallback

### Data Analysis
Go backend uses DuckDB to query Excel via SQL: 
```
CREATE TABLE sheet AS SELECT * FROM read_xlsx('file.xlsx')
```

---

## OCR Processing

### Architecture
**Key Principle**: docreader does NOT run OCR
- Classification only (text vs scanned pages)
- Image extraction
- Routing to Go backend

### PDF Page Classification
Signal: image-area coverage ratio (>= 50% = scanned)
- Text pages: extract native text layer
- Scanned pages: render to JPEG, route to OCR

### Image Extraction
From text pages:
- Min: 80x80 pixels
- Coverage: >= 1% of page
- Remove watermarks (>50% pages)
- Limit: 50 per document

### OCR Text Storage
Go backend stores in Image.OCRText field
Available in knowledge search via <image_ocr> tags

### Configuration
Environment variables (DOCREADER_PDF_* prefix):
- SCAN_IMAGE_RATIO=0.5
- EMBED_MIN_PIXELS=80
- etc

---

## Supported Formats

| Format | Engine | Features |
|--------|--------|----------|
| XLSX   | Excel/MarkItDown | Multi-sheet, merged cells |
| XLS    | Excel/MarkItDown | Legacy via xlrd |
| PDF    | PDF/MarkItDown | Page classification, OCR |
| DOCX   | Docx/MarkItDown | Full support |
| Images | Image | Base64 encoding |

---

## Key Files

- docreader/parser/excel_parser.py - Excel parsing
- docreader/parser/pdf_parser.py - PDF classification
- docreader/main.py - gRPC server
- internal/agent/tools/data_analysis.go - Excel queries

See full analysis in EXCEL_OCR_PROCESSING.md


