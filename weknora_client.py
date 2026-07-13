"""
WeKnora chat client with per-request LLM key + model fallback.

降级顺序（去掉了不稳定的 qwen3-4b-instruct）：
  anthropic::claude-4-8-opus  →  gpt-oss-120b  →  anthropic::claude-4-6-sonnet

切换触发条件（"上游报错就切"）：
  - HTTP 非 2xx
  - SSE 流里收到 response_type == "error"
  - 连接超时 / 网络异常
任一命中 → 换下一个模型重发；全部失败才抛出。
"""

import json
import requests

# ---- 配置区（按需改）----------------------------------------------------
BASE_URL = "http://10.47.202.30:8080"
PLATFORM_API_KEY = "sk-1IRFIgX0H5pOFedi2az-SUCuwoq9Q_Y0ptU1O8PqpGhZhO36"  # 平台 X-API-Key（所有人相同）
KB_ID = "db83823f-757d-4edb-a334-aa21fad3383a"

# 公共兜底 LLM key：当调用方没有自己的 key 时用它
PUBLIC_LLM_API_KEY = "e72f13d7-5711-4b90-a82e-fe665e02abf9"

# 模型降级链：主模型在前，备选依次靠后。名字必须带 provider 前缀，否则网关 404。
MODEL_FALLBACK_CHAIN = [
    "anthropic::claude-4-8-opus",    # 主：能力最强
    "gpt-oss-120b",                  # 备选 1
    "anthropic::claude-4-6-sonnet",  # 备选 2：更便宜
]

REQUEST_TIMEOUT = 120  # 单次请求秒数上限
# ------------------------------------------------------------------------


class UpstreamModelError(Exception):
    """某个模型这次不可用（HTTP 错误 / SSE error 事件 / 超时）。触发换下一个模型。"""


def create_session(kb_id: str = KB_ID) -> str:
    """建一个 session，返回 session_id。"""
    resp = requests.post(
        f"{BASE_URL}/api/v1/sessions",
        headers={"X-API-Key": PLATFORM_API_KEY, "Content-Type": "application/json"},
        json={"knowledge_base_id": kb_id},
        timeout=30,
    )
    resp.raise_for_status()
    return resp.json()["data"]["id"]


def _chat_once(session_id, query, model_name, llm_api_key, on_token):
    """
    用指定模型发一次 chat，逐 token 回调 on_token(text)。
    这个模型不可用时抛 UpstreamModelError（让上层换下一个）。
    成功返回完整答案字符串。
    """
    payload = {
        "query": query,
        "knowledge_base_ids": [KB_ID],
        "llm_api_key": llm_api_key,
        "model_name": model_name,
    }
    answer_parts = []
    try:
        with requests.post(
            f"{BASE_URL}/api/v1/knowledge-chat/{session_id}",
            headers={"X-API-Key": PLATFORM_API_KEY, "Content-Type": "application/json"},
            json=payload,
            stream=True,
            timeout=REQUEST_TIMEOUT,
        ) as resp:
            # HTTP 层错误 → 换模型
            if resp.status_code != 200:
                raise UpstreamModelError(f"HTTP {resp.status_code}: {resp.text[:200]}")

            for raw in resp.iter_lines(decode_unicode=True):
                if not raw or not raw.startswith("data:"):
                    continue
                try:
                    evt = json.loads(raw[len("data:"):].strip())
                except json.JSONDecodeError:
                    continue

                rtype = evt.get("response_type")

                # 上游 LLM 报错（如 401 无效 key、404 模型不存在、5xx）→ 换模型
                if rtype == "error":
                    raise UpstreamModelError(evt.get("content", "unknown upstream error"))

                # 正常答案 token
                if rtype == "answer":
                    piece = evt.get("content", "")
                    if piece:
                        answer_parts.append(piece)
                        if on_token:
                            on_token(piece)

    except (requests.Timeout, requests.ConnectionError) as e:
        raise UpstreamModelError(f"network: {e}") from e

    return "".join(answer_parts)


def chat(session_id, query, llm_api_key=None, on_token=None):
    """
    带自动降级的 chat。
    - llm_api_key: 调用方自己的 LLM key；传 None 则用公共兜底 key。
    - on_token(text): 可选，流式逐段回调。
    返回 (answer, used_model)。全部模型失败则抛 RuntimeError。
    """
    key = llm_api_key or PUBLIC_LLM_API_KEY
    last_err = None

    for model in MODEL_FALLBACK_CHAIN:
        try:
            answer = _chat_once(session_id, query, model, key, on_token)
            return answer, model
        except UpstreamModelError as e:
            last_err = e
            print(f"[fallback] 模型 {model} 不可用（{e}），切换下一个…")
            continue

    raise RuntimeError(f"所有模型都失败，最后错误：{last_err}")


if __name__ == "__main__":
    sid = create_session()
    print("session:", sid)

    ans, used = chat(
        sid,
        "你好，简单介绍一下这个知识库",
        llm_api_key="eab23662-d210-4e80-b333-0a9b3009fc14",  # 换成各自的 key；None=用公共 key
        on_token=lambda t: print(t, end="", flush=True),
    )
    print(f"\n\n---\n用的模型: {used}")
