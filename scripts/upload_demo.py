"""
WeKnora 文档上传工具 (纯标准库，无需 pip install)
串行模式：上传一个文件，等它解析完成后再上传下一个

用法:
  python3 upload_demo.py /path/to/file.pdf
  python3 upload_demo.py /path/to/folder/
"""

import os
import sys
import json
import ssl
import time
import mimetypes
from pathlib import Path
from urllib.request import Request, urlopen
from urllib.error import HTTPError, URLError
from uuid import uuid4

# ========== 配置 ==========
API_BASE = "http://10.47.202.30:8080/api/v1"
KB_ID = "968c86fa-ca5e-4470-8f17-248d5b639910"  # CommonDoc
LOGIN_EMAIL = "hanwzhan@qti.qualcomm.com"
LOGIN_PASSWORD = "Zhw897799"
POLL_INTERVAL = 5      # 轮询解析状态的间隔（秒）
POLL_TIMEOUT = 600     # 单个文件最长等待时间（秒），超时则跳过

SUPPORTED_EXTENSIONS = {
    ".pdf", ".docx", ".doc", ".xlsx", ".xls",
    ".pptx", ".ppt", ".txt", ".md", ".csv",
    ".html", ".htm", ".json", ".xml",
    ".png", ".jpg", ".jpeg",
    ".mp3", ".wav",
}

# 跳过 SSL 验证（企业内网）
CTX = ssl.create_default_context()
CTX.check_hostname = False
CTX.verify_mode = ssl.CERT_NONE


def http_json(url, data=None, headers=None, method="POST"):
    """发送 JSON 请求"""
    if headers is None:
        headers = {}
    if data is not None:
        headers["Content-Type"] = "application/json"
        body = json.dumps(data).encode("utf-8")
    else:
        body = None
    req = Request(url, data=body, headers=headers, method=method)
    try:
        resp = urlopen(req, timeout=30, context=CTX)
        return resp.status, json.loads(resp.read().decode("utf-8"))
    except HTTPError as e:
        try:
            return e.code, json.loads(e.read().decode("utf-8"))
        except:
            return e.code, {"message": str(e)}


def http_get(url, headers=None):
    """发送 GET 请求"""
    if headers is None:
        headers = {}
    req = Request(url, headers=headers, method="GET")
    try:
        resp = urlopen(req, timeout=30, context=CTX)
        return resp.status, json.loads(resp.read().decode("utf-8"))
    except HTTPError as e:
        try:
            return e.code, json.loads(e.read().decode("utf-8"))
        except:
            return e.code, {"message": str(e)}


def multipart_upload(url, file_path, token):
    """multipart/form-data 文件上传"""
    boundary = uuid4().hex
    file_name = os.path.basename(file_path)
    mime_type = mimetypes.guess_type(file_name)[0] or "application/octet-stream"

    with open(file_path, "rb") as f:
        file_data = f.read()

    body = (
        f"--{boundary}\r\n"
        f'Content-Disposition: form-data; name="file"; filename="{file_name}"\r\n'
        f"Content-Type: {mime_type}\r\n\r\n"
    ).encode("utf-8") + file_data + f"\r\n--{boundary}--\r\n".encode("utf-8")

    headers = {
        "Content-Type": f"multipart/form-data; boundary={boundary}",
        "Authorization": f"Bearer {token}",
    }
    req = Request(url, data=body, headers=headers, method="POST")
    try:
        resp = urlopen(req, timeout=120, context=CTX)
        return resp.status, json.loads(resp.read().decode("utf-8"))
    except HTTPError as e:
        try:
            return e.code, json.loads(e.read().decode("utf-8"))
        except:
            return e.code, {"message": str(e)}


def login():
    """登录获取 token"""
    url = f"{API_BASE}/auth/login"
    status, data = http_json(url, {"email": LOGIN_EMAIL, "password": LOGIN_PASSWORD})
    if status != 200:
        print(f"login failed (HTTP {status}): {data}")
        sys.exit(1)
    token = (
        data.get("data", {}).get("access_token", "") or
        data.get("data", {}).get("token", "") or
        data.get("access_token", "") or
        data.get("token", "")
    )
    if not token:
        print(f"no token in response: {json.dumps(data, ensure_ascii=False)}")
        sys.exit(1)
    print("login OK")
    return token


def wait_for_parse(knowledge_id, token):
    """轮询等待文档解析完成，返回最终状态"""
    url = f"{API_BASE}/knowledge/batch?ids={knowledge_id}"
    headers = {"Authorization": f"Bearer {token}"}
    start = time.time()

    while True:
        elapsed = time.time() - start
        if elapsed > POLL_TIMEOUT:
            print(f" TIMEOUT ({int(elapsed)}s)", flush=True)
            return "timeout"

        time.sleep(POLL_INTERVAL)
        try:
            status, data = http_get(url, headers)
        except Exception:
            continue

        if status != 200:
            continue

        items = data.get("data", [])
        if not items:
            continue

        parse_status = items[0].get("parse_status", "")
        if parse_status in ("completed", "failed"):
            return parse_status

        # 还在处理中，打印进度点
        print(".", end="", flush=True)


def upload_file(file_path, kb_id, token, wait=False):
    """上传单个文件，可选等待解析完成"""
    file_name = os.path.basename(file_path)
    file_size = os.path.getsize(file_path)
    print(f"  upload: {file_name} ({file_size / 1024:.1f} KB) ... ", end="", flush=True)

    url = f"{API_BASE}/knowledge-bases/{kb_id}/knowledge/file"
    status, data = multipart_upload(url, file_path, token)

    if status == 200:
        kid = data.get("data", {}).get("id", "")
        print(f"OK", end="", flush=True)
        if wait and kid:
            parse_result = wait_for_parse(kid, token)
            if parse_result == "completed":
                print(f" [completed]")
            elif parse_result == "failed":
                print(f" [FAILED]")
            else:
                print(f" [{parse_result}]")
        else:
            print()
        return True
    elif status == 409:
        code = data.get("code", "")
        msg = data.get("message", "conflict")
        if code == "newer_version_exists":
            print(f"SKIP newer version exists: {msg}")
        elif "duplicate" in code or "duplicate" in str(msg).lower():
            print(f"SKIP duplicate: {msg}")
        else:
            print(f"SKIP conflict: {msg}")
        return False
    elif status == 401:
        print(f"FAIL auth error - token expired?")
        return False
    else:
        msg = data.get("message", data.get("error", {}).get("message", str(data)))
        print(f"FAIL (HTTP {status}): {msg}")
        return False


def main():
    if len(sys.argv) < 2:
        print(f"Usage: python3 {sys.argv[0]} <file_or_folder> [kb_id]")
        sys.exit(1)

    target = Path(sys.argv[1])
    kb_id = sys.argv[2] if len(sys.argv) > 2 else KB_ID

    if not target.exists():
        print(f"path not found: {target}")
        sys.exit(1)

    print(f"server: {API_BASE}")
    print(f"kb_id:  {kb_id}")
    print(f"mode:   serial (wait for each file to complete)")
    print("=" * 40)

    token = login()
    print("=" * 40)

    if target.is_dir():
        files = sorted([
            f for f in target.rglob("*")
            if f.is_file() and f.suffix.lower() in SUPPORTED_EXTENSIONS
        ])
        if not files:
            print(f"no supported files in: {target}")
            sys.exit(1)
        print(f"found {len(files)} files\n")
        ok = 0
        failed = 0
        for i, f in enumerate(files, 1):
            print(f"[{i}/{len(files)}]", end="")
            if upload_file(str(f), kb_id, token, wait=True):
                ok += 1
            else:
                failed += 1
        print(f"\n{'=' * 40}")
        print(f"done: {ok} uploaded, {failed} skipped/failed, {len(files)} total")
    else:
        upload_file(str(target), kb_id, token, wait=True)


if __name__ == "__main__":
    main()
