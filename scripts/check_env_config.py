"""
Verify each entry in WeKnora's .env file for syntactic validity, key
length requirements, filesystem path existence, and live network
reachability of upstream services. Produces a tabulated report so a
quick glance tells you which variables are wired up correctly.

Usage:
    python scripts/check_env_config.py

Notes:
    - Network checks use a 3s TCP connect timeout; failures are not
      fatal (the report just flags them).
    - LLM/Embedding/Rerank endpoints get a tiny HTTP probe to /v1/models
      where applicable; any 2xx/4xx response is considered "reachable"
      because 401/404 still proves a server is listening.
"""

import os
import socket
import sqlite3
import sys
import urllib.request
import urllib.error
from pathlib import Path
from urllib.parse import urlparse


def parse_env(path):
    env = {}
    for raw in Path(path).read_text(encoding="utf-8").splitlines():
        line = raw.strip()
        if not line or line.startswith("#") or "=" not in line:
            continue
        k, _, v = line.partition("=")
        env[k.strip()] = v.strip()
    return env


def tcp_check(host, port, timeout=3.0):
    try:
        with socket.create_connection((host, port), timeout=timeout):
            return True, None
    except Exception as e:
        return False, str(e)


def http_probe(url, timeout=3.0):
    try:
        req = urllib.request.Request(url, method="GET")
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            return True, f"HTTP {resp.status}"
    except urllib.error.HTTPError as e:
        # 4xx/5xx still proves reachable
        return True, f"HTTP {e.code}"
    except Exception as e:
        return False, str(e)


def fmt(label, status, note=""):
    icon = {"OK": "[OK]", "WARN": "[WARN]", "FAIL": "[FAIL]", "INFO": "[--]"}.get(
        status, "[??]"
    )
    return f"  {icon:8} {label:35} {note}"


def section(title):
    print(f"\n=== {title} ===")


def main():
    env_path = ".env"
    if not Path(env_path).exists():
        print(f"ERROR: {env_path} not found", file=sys.stderr)
        sys.exit(1)

    env = parse_env(env_path)
    print(f"Loaded {len(env)} variables from {env_path}\n")

    # ---- Runtime ----
    section("Runtime")
    for k in ("GIN_MODE", "LOG_LEVEL", "WEKNORA_LANGUAGE", "TZ"):
        v = env.get(k, "")
        print(fmt(k, "OK" if v else "WARN", f"= {v!r}"))

    # ---- Crypto / Auth ----
    section("Crypto / Auth keys")
    for k, expected_len in (("SYSTEM_AES_KEY", 32), ("TENANT_AES_KEY", 32)):
        v = env.get(k, "")
        status = "OK" if len(v) == expected_len else "FAIL"
        print(fmt(k, status, f"len={len(v)} (need {expected_len})"))
    jwt = env.get("JWT_SECRET", "")
    status = "OK" if len(jwt) >= 16 else "WARN"
    print(fmt("JWT_SECRET", status, f"len={len(jwt)} (recommend >=32)"))

    # ---- Database ----
    section("Database")
    drv = env.get("DB_DRIVER", "")
    print(fmt("DB_DRIVER", "OK" if drv else "FAIL", f"= {drv!r}"))
    if drv == "sqlite":
        dbp = env.get("DB_PATH", "")
        p = Path(dbp).resolve()
        exists = p.exists()
        print(
            fmt(
                "DB_PATH",
                "OK" if exists else "WARN",
                f"= {dbp} -> {p} ({'exists' if exists else 'missing'})",
            )
        )
        if exists:
            try:
                conn = sqlite3.connect(p)
                cur = conn.cursor()
                cur.execute(
                    "SELECT count(*) FROM sqlite_master WHERE type='table'"
                )
                t = cur.fetchone()[0]
                cur.execute("SELECT count(*) FROM users")
                u = cur.fetchone()[0]
                cur.execute("SELECT count(*) FROM tenants")
                tn = cur.fetchone()[0]
                cur.execute(
                    "SELECT count(*) FROM models WHERE is_builtin=1"
                )
                m = cur.fetchone()[0]
                conn.close()
                print(
                    fmt(
                        "  -> tables/users/tenants/builtin_models",
                        "INFO",
                        f"{t} / {u} / {tn} / {m}",
                    )
                )
            except Exception as e:
                print(fmt("  -> sqlite probe", "WARN", str(e)))

    # ---- Models (LLM / Embedding / Rerank) ----
    section("Built-in models (config/builtin_models.yaml uses these)")
    for prefix in ("LLM", "EMBEDDING", "RERANK"):
        name = env.get(f"{prefix}_MODEL_NAME", "")
        url = env.get(f"{prefix}_BASE_URL", "")
        api = env.get(f"{prefix}_API_KEY", "")
        prov = env.get(f"{prefix}_PROVIDER", "")
        print(fmt(f"{prefix}_MODEL_NAME", "OK" if name else "FAIL", f"= {name!r}"))
        print(fmt(f"{prefix}_BASE_URL", "OK" if url else "FAIL", f"= {url!r}"))
        print(
            fmt(
                f"{prefix}_API_KEY",
                "OK" if api else "WARN",
                f"len={len(api)}",
            )
        )
        print(fmt(f"{prefix}_PROVIDER", "OK" if prov else "WARN", f"= {prov!r}"))
        # Probe the base URL host:port
        if url:
            u = urlparse(url)
            host = u.hostname or ""
            port = u.port or (443 if u.scheme == "https" else 80)
            if host:
                ok, msg = tcp_check(host, port)
                print(
                    fmt(
                        f"  -> tcp {host}:{port}",
                        "OK" if ok else "WARN",
                        msg if msg else "reachable",
                    )
                )
                # try /v1/models
                probe_url = url.rstrip("/") + "/models"
                ok2, msg2 = http_probe(probe_url)
                print(
                    fmt(
                        f"  -> GET {probe_url}",
                        "OK" if ok2 else "WARN",
                        msg2,
                    )
                )

    # ---- Vector store ----
    section("Vector store / Retriever")
    rd = env.get("RETRIEVE_DRIVER", "")
    print(fmt("RETRIEVE_DRIVER", "OK" if rd else "FAIL", f"= {rd!r}"))
    if "milvus" in rd.lower():
        addr = env.get("MILVUS_ADDRESS", "")
        if addr:
            try:
                host, port_s = addr.split(":")
                port = int(port_s)
                ok, msg = tcp_check(host, port)
                print(
                    fmt(
                        f"MILVUS_ADDRESS {addr}",
                        "OK" if ok else "WARN",
                        msg if msg else "reachable",
                    )
                )
            except ValueError:
                print(fmt("MILVUS_ADDRESS", "FAIL", f"malformed: {addr!r}"))
        for k in ("MILVUS_COLLECTION", "MILVUS_METRIC_TYPE"):
            v = env.get(k, "")
            print(fmt(k, "OK" if v else "WARN", f"= {v!r}"))

    # ---- Stream manager / Redis ----
    section("Stream manager / Redis")
    sm = env.get("STREAM_MANAGER_TYPE", "")
    print(fmt("STREAM_MANAGER_TYPE", "OK" if sm else "WARN", f"= {sm!r}"))
    if sm == "redis":
        for k in ("REDIS_PASSWORD", "REDIS_DB", "REDIS_PREFIX"):
            v = env.get(k, "")
            print(fmt(k, "OK" if v else "WARN", "set" if v else "missing"))
    else:
        print(fmt("Redis vars", "INFO", "ignored (in-memory stream)"))

    # ---- Storage ----
    section("File storage")
    st = env.get("STORAGE_TYPE", "")
    print(fmt("STORAGE_TYPE", "OK" if st else "FAIL", f"= {st!r}"))
    if st == "local":
        base = env.get("LOCAL_STORAGE_BASE_DIR", "")
        p = Path(base).resolve()
        exists = p.exists()
        print(
            fmt(
                "LOCAL_STORAGE_BASE_DIR",
                "OK" if exists else "WARN",
                f"{base} -> {p} ({'exists' if exists else 'missing'})",
            )
        )

    # ---- DocReader ----
    section("DocReader (document parser)")
    da = env.get("DOCREADER_ADDR", "")
    dt = env.get("DOCREADER_TRANSPORT", "")
    print(fmt("DOCREADER_ADDR", "OK" if da else "WARN", f"= {da!r}"))
    print(fmt("DOCREADER_TRANSPORT", "OK" if dt else "WARN", f"= {dt!r}"))
    if da:
        try:
            host, port_s = da.split(":")
            port = int(port_s)
            ok, msg = tcp_check(host, port)
            print(
                fmt(
                    f"  -> tcp {host}:{port}",
                    "OK" if ok else "WARN",
                    msg if msg else "reachable",
                )
            )
            if not ok and host == "docreader":
                print(
                    fmt(
                        "  -> note",
                        "WARN",
                        "'docreader' is the docker-compose hostname; not "
                        "resolvable when running WeKnora.exe natively. "
                        "Use 127.0.0.1:50051 with a local docreader.",
                    )
                )
        except ValueError:
            print(fmt("DOCREADER_ADDR", "FAIL", f"malformed: {da!r}"))

    # ---- Sandbox / Concurrency / Misc ----
    section("Sandbox / Misc")
    for k in (
        "WEKNORA_SANDBOX_MODE",
        "WEKNORA_SANDBOX_TIMEOUT",
        "CONCURRENCY_POOL_SIZE",
        "OLLAMA_BASE_URL",
        "DISABLE_REGISTRATION",
        "ENABLE_GRAPH_RAG",
        "NEO4J_ENABLE",
        "FRONTEND_PORT",
        "APP_PORT",
    ):
        v = env.get(k, "")
        print(fmt(k, "OK" if v else "INFO", f"= {v!r}"))

    print("\nDone.")


if __name__ == "__main__":
    main()