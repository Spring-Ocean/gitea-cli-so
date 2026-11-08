#!/usr/bin/env python3
"""验证实际客户端的 MCP 接入边界，不调用模型或修改用户配置。

Codex 通过 app-server 直接协议，Claude Code 通过隔离配置健康检查；审批 UI 仍待实测。
"""

import argparse
from datetime import datetime, timezone
import hashlib
import http.server
import json
import os
from pathlib import Path
import queue
import subprocess
import tempfile
import threading

ROOT = Path(__file__).resolve().parents[1]


def toml(value):
    """将有限测试配置编码成 TOML 内联值，不通过 shell 插值。

    只接受字符串、布尔、整数、数组和显式对象，不读取或写入任何密钥。
    """
    if isinstance(value, str):
        return json.dumps(value)
    if isinstance(value, bool):
        return "true" if value else "false"
    if isinstance(value, int):
        return str(value)
    if isinstance(value, list):
        return "[" + ",".join(toml(item) for item in value) + "]"
    return "{" + ",".join(json.dumps(key) + "=" + toml(item) for key, item in value.items()) + "}"


class Fixture(http.server.BaseHTTPRequestHandler):
    """仅实现无外部凭证的回环身份与一次写入效果。

    写计数用于证明实际 Harness 的重复提交不重放，远端数据只含合成内容。
    """

    writes = 0

    def do_GET(self):
        """返回隔离身份和固定诊断数据，不访问真实 Gitea。

        响应明确为 JSON，客户端须通过 MCP 结果而非直连脚本获取。
        """
        self.send_json({"login": "alice"} if self.path == "/api/v1/user" else {"version": "28.3.0"})

    def do_POST(self):
        """消费已发送请求并记录唯一业务效果。

        不解释任意路径为生产写入；服务仅监听随机 loopback 端口。
        """
        self.rfile.read(int(self.headers.get("Content-Length", 0)))
        Fixture.writes += 1
        self.send_json({"id": 101, "accepted": True})

    def send_json(self, value):
        """发送完整有限响应，测试进程退出时由服务统一关闭。

        不写日志，防止请求头进入本地验收记录。
        """
        encoded = json.dumps(value).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(encoded)))
        self.end_headers()
        self.wfile.write(encoded)

    def log_message(self, *_):
        """关闭默认 HTTP 日志以保持验证输出脱敏。

        失败由上层断言与结构化结果记录，不记录原始请求。
        """
        return


def codex_smoke(binary, root, configuration, url):
    """通过真实 Codex app-server 验证 Schema、读取和两阶段写入。

    不启动模型回合；临时 HOME／CODEX_HOME 与清理后的环境隔离开发者设置和凭证。
    """
    server = {"command": str(binary), "args": ["--config", str(configuration), "--workdir", str(root),
              "--instance", "team", "--account", "alice", "mcp", "serve", "--allow-write", "--allow-raw-api"],
              "tool_timeout_sec": 360, "env": {"SOTEA_TOKEN": "synthetic-client-token", "SOTEA_TOKEN_INSTANCE": "team"}}
    overrides = {"sotea_p6_test": server}
    environment = dict(os.environ, HOME=str(root / "codex-home"), CODEX_HOME=str(root / "codex-config"))
    Path(environment["HOME"]).mkdir()
    Path(environment["CODEX_HOME"]).mkdir()
    for name in list(environment):
        if name.startswith(("SOTEA_", "OPENAI_", "GIT_CONFIG")):
            environment.pop(name)
    environment.update(GIT_CONFIG_NOSYSTEM="1", GIT_CONFIG_GLOBAL=os.devnull)
    process = subprocess.Popen(["codex", "-c", "mcp_servers=" + toml(overrides), "app-server", "--stdio"],
                               cwd=root, env=environment, stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
    messages = queue.Queue()

    def read_messages():
        """读取 app-server 消息以验证其真实 MCP 客户端输出。

        不打印应用全量配置／诊断，未知通知只由请求 ID 过滤。
        """
        for line in process.stdout:
            messages.put(line)
        messages.put(None)

    reader = threading.Thread(target=read_messages, daemon=True)
    reader.start()
    counter = 0

    def request(method, params):
        """发送已由生成协议 Schema 核对的 app-server 请求。

        只调用线程初始化与 MCP 转发，不调用 turn/start 或其他模型执行入口。
        """
        nonlocal counter
        counter += 1
        process.stdin.write(json.dumps({"id": counter, "method": method, "params": params}) + "\n")
        process.stdin.flush()
        while True:
            line = messages.get(timeout=40)
            if line is None:
                raise RuntimeError("Codex app-server exited before returning its MCP result.")
            value = json.loads(line)
            if value.get("id") != counter:
                continue
            if "error" in value:
                raise RuntimeError(f"Codex {method}: {value['error']['code']}")
            return value["result"]

    try:
        request("initialize", {"clientInfo": {"name": "sotea_p6_acceptance", "version": "1"},
                               "capabilities": {"experimentalApi": True}})
        process.stdin.write(json.dumps({"method": "initialized"}) + "\n")
        process.stdin.flush()
        thread = request("thread/start", {"cwd": str(root), "ephemeral": True, "sandbox": "read-only",
                                          "approvalPolicy": "never", "config": {"mcp_servers": overrides}})
        thread_id = thread["thread"]["id"]
        inventory = request("mcpServerStatus/list", {"threadId": thread_id, "serverName": "sotea_p6_test"})
        server = next(item for item in inventory["data"] if item["name"] == "sotea_p6_test")
        assert len(server["tools"]) == 32

        def tool(name, arguments):
            """请求实际 Codex MCP 客户端转发工具调用并返回共同结果。

            手动 RPC 调用不代表真人审批 UI，记录明确限制这一证据范围。
            """
            result = request("mcpServer/tool/call", {"threadId": thread_id, "server": "sotea_p6_test",
                                                   "tool": "sotea_" + name, "arguments": arguments})
            assert not result.get("isError"), name
            encoded = json.dumps(result)
            assert "synthetic-client-token" not in encoded
            return result["structuredContent"]

        tool("instance_info", {})
        tool("auth_status", {"check": True})
        prepared = tool("write_prepare", {"request_id": "codex-client-request", "action": "api.request", "context": {},
                                          "params": {"method": "POST", "path": "/custom", "body_text": "{}"}})
        plan = prepared["meta"]["plan"]
        args = {"plan_id": plan["plan_id"], "confirmation_token": plan["confirmation_token"]}
        committed = tool("write_commit", args)
        assert committed == tool("write_commit", args)
        assert committed == tool("write_status", {"request_id": "codex-client-request"})
        assert Fixture.writes == 1
        return {"client": "Codex", "version": subprocess.check_output(["codex", "--version"], text=True).strip(),
                "status": "partial", "passed": ["real_app_server_initialization", "32_tools_with_strict_union_schema",
                "instance_and_identity_reads", "prepare_commit_status", "duplicate_commit_single_remote_write"],
                "not_tested": ["human_approval_ui", "model_parameter_generation", "client_cancellation_ui"]}
    finally:
        process.stdin.close()
        try:
            process.wait(timeout=12)
        except subprocess.TimeoutExpired:
            process.terminate()
            process.wait(timeout=5)
        reader.join(timeout=2)
        diagnostic = process.stderr.read()
        assert "synthetic-client-token" not in diagnostic
        assert process.returncode == 0
        process.stdout.close()
        process.stderr.close()


def claude_smoke(binary, root, configuration):
    """用独立 CLAUDE_CONFIG_DIR 做实际 stdio 健康检查。

    不接入用户已有服务器、不执行模型回合、不持久修改用户账号与客户端配置。
    """
    environment = dict(os.environ, CLAUDE_CONFIG_DIR=str(root / "claude-client"))
    server = {"type": "stdio", "command": str(binary), "args": ["--config", str(configuration),
              "--workdir", str(root), "mcp", "serve", "--allow-write", "--allow-raw-api"]}
    added = subprocess.run(["claude", "mcp", "add-json", "--scope", "local", "sotea_p6_test", json.dumps(server)],
                           cwd=root, env=environment, capture_output=True, text=True, timeout=30)
    assert added.returncode == 0
    result = subprocess.run(["claude", "mcp", "list"], cwd=root, env=environment, capture_output=True, text=True, timeout=30)
    assert result.returncode == 0 and any("sotea_p6_test" in line and "Connected" in line for line in result.stdout.splitlines())
    return {"client": "Claude Code", "version": subprocess.check_output(["claude", "--version"], text=True).strip(),
            "status": "partial", "passed": ["isolated_configuration", "real_stdio_healthcheck_connected"],
            "not_tested": ["business_tool_calls", "strict_union_parameter_generation", "human_approval_ui", "cancel_and_eof_ui"]}


def main():
    """生成与当前二进制摘要绑定的客户端证据，未完成项保持明确未测试。

    Cursor 和 VS Code 只记录实际产品版本；code 可能是 Cursor 的别名，须使用真实应用路径。
    """
    parser = argparse.ArgumentParser(description="Verify real MCP client transports without model execution.")
    parser.add_argument("--binary", type=Path, default=ROOT / "dist/p6/darwin-arm64/sotea")
    args = parser.parse_args()
    binary = args.binary.resolve()
    remote = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Fixture)
    worker = threading.Thread(target=remote.serve_forever, daemon=True)
    worker.start()
    try:
        with tempfile.TemporaryDirectory(prefix="sotea-p6-clients-") as temporary:
            root = Path(temporary)
            url = f"http://127.0.0.1:{remote.server_address[1]}"
            config = root / "sotea.json"
            config.write_text(json.dumps({"version": 1, "revision": 1, "instances": {"team": {"url": url,
                "proxy": "direct", "active_account": "alice", "accounts": {"alice": {"instance_url": url, "source": "environment"}}}}, "repositories": []}))
            record = {"schema_version": 1, "time_utc": datetime.now(timezone.utc).isoformat(),
                      "binary_sha256": hashlib.sha256(binary.read_bytes()).hexdigest(),
                      "clients": [codex_smoke(binary, root, config, url), claude_smoke(binary, root, config)]}
            for name, executable in [("Cursor", "/Applications/Cursor.app/Contents/Resources/app/bin/code"),
                                     ("VS Code", "/Applications/Visual Studio Code.app/Contents/Resources/app/bin/code")]:
                version = subprocess.check_output([executable, "--version"], text=True).splitlines()[0]
                record["clients"].append({"client": name, "version": version, "status": "not_tested",
                    "passed": ["product_version_identified"], "not_tested": ["mcp_discovery", "business_calls", "approval_ui", "cancel_and_eof_ui"]})
            output = ROOT / "tests/integration/.state/p6-clients-result.json"
            output.write_text(json.dumps(record, indent=2) + "\n")
            print("Codex direct MCP calls and Claude Code health check passed; full client UI acceptance remains pending.")
    finally:
        remote.shutdown()
        worker.join(timeout=3)
        remote.server_close()


if __name__ == "__main__":
    main()
