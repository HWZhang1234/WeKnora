"""
WeKnora usid 权限 + Chat Demo

功能：
  1. 输入 usid，查询其有权限的知识库列表
  2. 选择知识库（或不选，搜全部有权限的），发起问答
  3. 支持多轮对话，输入 'quit' 退出，输入 'switch' 切换 usid
"""

import json
import requests

BASE_URL = "http://10.47.202.30:8080"
API_KEY  = "sk-1IRFIgX0H5pOFedi2az-SUCuwoq9Q_Y0ptU1O8PqpGhZhO36"
HEADERS_BASE = {
    "X-API-Key": API_KEY,
    "Content-Type": "application/json",
}


# ── 1. 查询 usid 可访问的知识库 ──────────────────────────────────────────────

def list_searchable_kbs(usid: str) -> list[dict]:
    resp = requests.get(
        f"{BASE_URL}/api/v1/permissions/searchable-kbs",
        headers=HEADERS_BASE,
        params={"usid": usid},
        timeout=15,
    )
    if resp.status_code == 200:
        return resp.json().get("data", [])
    print(f"  [错误] 查询权限失败: HTTP {resp.status_code} — {resp.text[:200]}")
    return []


# ── 2. 创建会话 ───────────────────────────────────────────────────────────────

def create_session(usid: str) -> str | None:
    resp = requests.post(
        f"{BASE_URL}/api/v1/sessions",
        headers={**HEADERS_BASE, "X-User-Id": usid},
        json={},
        timeout=15,
    )
    if resp.status_code == 201:
        return resp.json()["data"]["id"]
    print(f"  [错误] 创建会话失败: HTTP {resp.status_code} — {resp.text[:200]}")
    return None


# ── 3. 流式问答 ───────────────────────────────────────────────────────────────

def chat(session_id: str, usid: str, query: str, kb_ids: list[str]) -> bool:
    payload: dict = {
        "query": query,
        "agent_id": "builtin-quick-answer",
    }
    if kb_ids:
        payload["knowledge_base_ids"] = kb_ids

    try:
        with requests.post(
            f"{BASE_URL}/api/v1/knowledge-chat/{session_id}",
            headers={**HEADERS_BASE, "X-User-Id": usid},
            json=payload,
            stream=True,
            timeout=120,
        ) as resp:
            if resp.status_code == 401:
                print("  [错误 401] 未提供 X-User-Id")
                return False
            if resp.status_code == 403:
                print("  [错误 403] usid 无权访问所请求的知识库")
                return False
            if resp.status_code != 200:
                print(f"  [错误 {resp.status_code}] {resp.text[:200]}")
                return False

            print("\n  助手: ", end="", flush=True)
            for raw in resp.iter_lines(decode_unicode=True):
                if not raw or not raw.startswith("data:"):
                    continue
                try:
                    evt = json.loads(raw[len("data:"):].strip())
                except json.JSONDecodeError:
                    continue

                rtype = evt.get("response_type")
                if rtype == "references":
                    refs = evt.get("knowledge_references") or []
                    if refs:
                        titles = ", ".join(
                            r.get("knowledge_title", r.get("knowledge_id", "?"))
                            for r in refs
                        )
                        print(f"\n  [引用来源: {titles}]", flush=True)
                        print("  助手: ", end="", flush=True)
                elif rtype == "answer":
                    piece = evt.get("content", "")
                    if piece:
                        print(piece, end="", flush=True)
                elif rtype == "error":
                    print(f"\n  [上游错误] {evt.get('content', '')}")
                    return False

            print()  # 换行
            return True

    except requests.Timeout:
        print("\n  [错误] 请求超时")
        return False
    except requests.ConnectionError as e:
        print(f"\n  [错误] 连接失败: {e}")
        return False


# ── 4. 主流程 ─────────────────────────────────────────────────────────────────

SOURCE_LABEL = {"acl": "授权", "common": "公共", "super": "super_user"}

def show_kbs(kbs: list[dict]):
    if not kbs:
        print("  （该 usid 没有可访问的知识库）")
        return
    print(f"  {'#':<4} {'KB ID':<40} {'名称':<20} {'来源'}")
    print("  " + "-" * 75)
    for i, kb in enumerate(kbs, 1):
        source = SOURCE_LABEL.get(kb.get("source", ""), kb.get("source", ""))
        role   = f"({kb['role']})" if kb.get("role") else ""
        print(f"  {i:<4} {kb['kb_id']:<40} {kb.get('name',''):<20} {source}{role}")


def select_kbs(kbs: list[dict]) -> list[str]:
    if not kbs:
        return []
    print("\n  选择要搜索的知识库（多个用逗号分隔，直接回车=搜全部有权限的）:")
    raw = input("  > ").strip()
    if not raw:
        return []
    selected = []
    for tok in raw.split(","):
        tok = tok.strip()
        if tok.isdigit():
            idx = int(tok) - 1
            if 0 <= idx < len(kbs):
                selected.append(kbs[idx]["kb_id"])
            else:
                print(f"  [警告] 序号 {tok} 超出范围，已跳过")
        else:
            selected.append(tok)  # 也可以直接粘 kb_id
    return selected


def run():
    print("=" * 60)
    print("  WeKnora usid 权限 + Chat Demo")
    print("  输入 'quit' 退出  |  输入 'switch' 切换 usid")
    print("=" * 60)

    while True:
        # ── 选择 usid ──
        usid = input("\n请输入 usid（Qualcomm NTID，如 hanwzhan）: ").strip()
        if usid.lower() == "quit":
            break
        if not usid:
            continue

        # ── 查权限 ──
        print(f"\n正在查询 {usid} 的可访问知识库…")
        kbs = list_searchable_kbs(usid)
        show_kbs(kbs)

        if not kbs:
            print("无可用知识库，请换一个 usid 或联系管理员授权。")
            continue

        # ── 选知识库 ──
        selected_kb_ids = select_kbs(kbs)
        scope_desc = (
            "、".join(
                next((k["name"] for k in kbs if k["kb_id"] == kid), kid)
                for kid in selected_kb_ids
            ) if selected_kb_ids else "全部有权限的知识库"
        )
        print(f"\n检索范围: {scope_desc}")

        # ── 创建会话 ──
        print("正在创建会话…")
        session_id = create_session(usid)
        if not session_id:
            continue
        print(f"会话已创建: {session_id}")

        # ── 多轮问答 ──
        print("\n开始对话（输入 'quit' 退出，'switch' 切换 usid，'kbs' 重新选知识库）\n")
        while True:
            query = input("  你: ").strip()
            if not query:
                continue
            if query.lower() == "quit":
                return
            if query.lower() == "switch":
                break
            if query.lower() == "kbs":
                selected_kb_ids = select_kbs(kbs)
                scope_desc = (
                    "、".join(
                        next((k["name"] for k in kbs if k["kb_id"] == kid), kid)
                        for kid in selected_kb_ids
                    ) if selected_kb_ids else "全部有权限的知识库"
                )
                print(f"  检索范围已切换为: {scope_desc}\n")
                continue

            chat(session_id, usid, query, selected_kb_ids)

    print("\n已退出。")


if __name__ == "__main__":
    run()
