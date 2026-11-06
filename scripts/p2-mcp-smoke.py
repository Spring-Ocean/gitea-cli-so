#!/usr/bin/env python3
"""验证 P2 真实 stdio 子进程、工具发现、配置失效和 EOF。

仅访问固定 loopback 的 Gitea 版本入口，不使用用户凭证或客户端配置。
"""

import argparse
from datetime import datetime, timezone
import hashlib
import json
import os
from pathlib import Path
import queue
import subprocess
import tempfile
import threading

ROOT = Path(__file__).resolve().parents[1]


def smoke(binary, protocol):
    """通过真实行分隔 JSON-RPC 与独立 sotea 进程往返。

    每个协议使用全新配置；服务应在 EOF 后自行退出并保持 stderr 无普通日志。
    """
    with tempfile.TemporaryDirectory(prefix="sotea-p2-mcp-") as directory:
        configuration = Path(directory) / "config.json"
        config = {"version": 1, "revision": 1, "instances": {
            "isolated": {"url": "http://127.0.0.1:32830", "proxy": "direct"}}, "repositories": []}
        configuration.write_text(json.dumps(config))
        environment = dict(os.environ)
        for name in ("SOTEA_TOKEN", "SOTEA_TOKEN_INSTANCE", "SOTEA_INSTANCE", "SOTEA_ACCOUNT"):
            environment.pop(name, None)
        process = subprocess.Popen([str(binary), "--config", str(configuration), "mcp", "serve"],
                                   env=environment, stdin=subprocess.PIPE, stdout=subprocess.PIPE,
                                   stderr=subprocess.PIPE, text=True)
        messages = queue.Queue()

        def read_messages():
            """将协议行放入有界超时可等待的消息队列。

            进程退出后管道 EOF 结束线程，不解析普通终端文本为协议。
            """
            for line in process.stdout:
                messages.put(line)
            messages.put(None)

        reader = threading.Thread(target=read_messages, daemon=True)
        reader.start()
        counter = 0

        def request(method, params=None):
            """发送一个请求并等待匹配 id，允许忽略协议通知。

            非 JSON stdout、提前 EOF 或超时均视为失败。
            """
            nonlocal counter
            counter += 1
            payload = {"jsonrpc": "2.0", "id": counter, "method": method}
            if params is not None:
                payload["params"] = params
            process.stdin.write(json.dumps(payload) + "\n")
            process.stdin.flush()
            while True:
                line = messages.get(timeout=15)
                if line is None:
                    raise RuntimeError("MCP exited before responding.")
                value = json.loads(line)
                if value.get("id") == counter:
                    return value

        def tool(**arguments):
            """调用唯一诊断工具并读取完整结构化业务结果。

            错误工具调用不应导致协议或后续调用失效。
            """
            return request("tools/call", {"name": "sotea_instance_info", "arguments": arguments})

        try:
            initialized = request("initialize", {"protocolVersion": protocol, "capabilities": {},
                                  "clientInfo": {"name": "sotea-p2-smoke", "version": "1"}})
            assert initialized["result"]["protocolVersion"] == protocol
            process.stdin.write(json.dumps({"jsonrpc": "2.0", "method": "notifications/initialized"}) + "\n")
            process.stdin.flush()
            tools = request("tools/list")["result"]["tools"]
            assert len(tools) == 1 and tools[0]["name"] == "sotea_instance_info"
            assert tools[0]["inputSchema"]["additionalProperties"] is False
            first = tool(instance="isolated")["result"]
            assert not first.get("isError") and first["structuredContent"]["status"] == "success"
            info = first["structuredContent"]["items"][0]["data"]
            assert info["raw_version"] == "28.3.0" and info["instance_evidence"] == "not_tested"
            cached = tool(instance="isolated")["result"]["structuredContent"]["items"][0]["data"]
            assert cached["cached"]
            invalid = tool(instance="isolated", unexpected=True)
            assert "error" in invalid or invalid["result"].get("isError")
            failed = tool(instance="unregistered-name")["result"]
            assert failed.get("isError") and failed["structuredContent"]["status"] == "failed"
            config["revision"] = 2
            configuration.write_text(json.dumps(config))
            fresh = tool(instance="isolated")["result"]["structuredContent"]["items"][0]["data"]
            assert not fresh["cached"]
            process.stdin.close()
            assert process.wait(timeout=5) == 0
            reader.join(timeout=2)
            assert not reader.is_alive()
            assert process.stderr.read() == ""
            return {"protocol": protocol, "status": "passed", "checks": [
                "initialize", "strict_tools_list", "structured_diagnostic", "cached_read",
                "unknown_argument_rejected", "business_error_keeps_server_alive",
                "config_revision_invalidates_cache", "eof_exit_without_stdout_noise"]}
        finally:
            if process.poll() is None:
                process.kill()
                process.wait(timeout=5)
            for stream in (process.stdout, process.stderr):
                stream.close()


def main():
    """分别验证两个明确支持的 MCP 协议版本并归档脱敏证据。

    此记录属于官方 SDK 协议验证，不代替 Codex 等真实客户端验收。
    """
    parser = argparse.ArgumentParser(description="Verify the P2 MCP stdio diagnostic prototype.")
    parser.add_argument("--binary", type=Path, default=ROOT / "dist/p2/darwin-arm64/sotea")
    args = parser.parse_args()
    binary = args.binary.resolve()
    result = {"schema_version": 1, "time_utc": datetime.now(timezone.utc).isoformat(),
              "binary_sha256": hashlib.sha256(binary.read_bytes()).hexdigest(),
              "protocols": [smoke(binary, version) for version in ("2025-11-25", "2025-06-18")]}
    path = ROOT / "tests/integration/.state/p2-mcp-result.json"
    path.write_text(json.dumps(result, indent=2) + "\n")
    print("P2 MCP stdio, strict schema, configuration refresh, and EOF checks passed.")


if __name__ == "__main__":
    main()
