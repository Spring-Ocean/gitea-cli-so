#!/usr/bin/env python3
"""验证真实 Codex 会话向同一用户级 MCP 配置提供不同项目根。

不调用模型、不使用远端凭证；通过临时 HOME／CODEX_HOME 的 app-server 验证实际 MCP 转发。
"""
import argparse
from datetime import datetime, timezone
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import queue
import shutil
import subprocess
import sys
import tempfile
import threading

ROOT = Path(__file__).resolve().parents[1]
SPEC = importlib.util.spec_from_file_location("p6_clients", ROOT / "scripts/p6-client-smoke.py")
HELPERS = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(HELPERS)


def codex_workspace(binary, root):
    """在启动目录与两个项目都不同的条件下检查真实 Codex 工作区传递。

    同一配置不写 cwd／workdir／repo；两个 thread 的本地根必须分别来自 thread/start 的 cwd。
    """
    configuration = root / "sotea.json"
    configuration.write_text(json.dumps({"version": 1, "instances": {"team": {
        "url": "http://127.0.0.1:1", "active_account": "alice",
        "accounts": {"alice": {"source": "environment", "instance_url": "http://127.0.0.1:1"}}
    }}, "repositories": []}))
    projects = [root / "project-a", root / "project b"]
    environment = dict(os.environ, HOME=str(root / "home"), CODEX_HOME=str(root / "codex-home"),
                       GIT_CONFIG_GLOBAL=os.devnull, GIT_CONFIG_NOSYSTEM="1")
    Path(environment["HOME"]).mkdir()
    Path(environment["CODEX_HOME"]).mkdir()
    for name in ["SOTEA_TOKEN", "SOTEA_TOKEN_INSTANCE", "SOTEA_ACCOUNT", "SOTEA_REPO", "SOTEA_INSTANCE", "OPENAI_API_KEY"]:
        environment.pop(name, None)
    for index, project in enumerate(projects):
        subprocess.run(["git", "init", "--quiet", str(project)], env=environment, check=True)
        subprocess.run(["git", "-C", str(project), "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid",
                        "-c", "commit.gpgsign=false", "-c", "core.hooksPath=", "commit", "--quiet", "--allow-empty", "-m", "fixture"],
                       env=environment, check=True)
        subprocess.run(["git", "-C", str(project), "remote", "add", "origin", "http://127.0.0.1:1/team/project-" + ("a" if index == 0 else "b")],
                       env=environment, check=True)
    server = {"command": str(binary), "args": ["--config", str(configuration), "--instance", "team", "--account", "alice",
              "mcp", "serve", "--follow-workspace", "--workspace-source", "codex"], "tool_timeout_sec": 40}
    overrides = {"sotea_workspace_test": server}
    code = shutil.which("codex")
    if not code:
        raise RuntimeError("The real Codex CLI is unavailable.")
    version = subprocess.check_output([code, "--version"], text=True).strip()
    process = subprocess.Popen([code, "-c", "mcp_servers=" + HELPERS.toml(overrides), "app-server", "--stdio"],
                               cwd=root, env=environment, stdin=subprocess.PIPE, stdout=subprocess.PIPE,
                               stderr=subprocess.DEVNULL, text=True)
    messages = queue.Queue()

    def read_messages():
        """有界请求队列只接收 app-server 的 JSON 消息。

        不输出客户端设置或会话内容；异常由请求方法名及错误码识别。
        """
        for line in process.stdout:
            messages.put(line)
        messages.put(None)

    reader = threading.Thread(target=read_messages, daemon=True)
    reader.start()
    counter = 0

    def request(method, params):
        """发送无模型 app-server 请求并等待匹配 ID。

        等待受 40 秒上限限制，服务端错误不伪装为成功或自动尝试其他工作区。
        """
        nonlocal counter
        counter += 1
        process.stdin.write(json.dumps({"id": counter, "method": method, "params": params}) + "\n")
        process.stdin.flush()
        while True:
            line = messages.get(timeout=40)
            if line is None:
                raise RuntimeError("Codex exited before its MCP workspace result.")
            value = json.loads(line)
            if value.get("id") != counter:
                continue
            if "error" in value:
                raise RuntimeError(f"Codex {method}: {value['error']['code']}")
            return value["result"]

    results = []
    try:
        request("initialize", {"clientInfo": {"name": "sotea_workspace_acceptance", "version": "1"},
                               "capabilities": {"experimentalApi": True}})
        process.stdin.write(json.dumps({"method": "initialized"}) + "\n")
        process.stdin.flush()
        for project in projects:
            thread = request("thread/start", {"cwd": str(project), "ephemeral": True, "sandbox": "read-only",
                                              "approvalPolicy": "never", "config": {"mcp_servers": overrides}})
            thread_id = thread["thread"]["id"]
            inventory = request("mcpServerStatus/list", {"threadId": thread_id, "serverName": "sotea_workspace_test"})
            item = next(item for item in inventory["data"] if item["name"] == "sotea_workspace_test")
            if len(item["tools"]) != 28:
                raise RuntimeError("The follow-workspace server did not expose its local Git tool.")
            result = request("mcpServer/tool/call", {"threadId": thread_id, "server": "sotea_workspace_test",
                                                    "tool": "sotea_git_context", "arguments": {}})
            if result.get("isError"):
                content = result.get("structuredContent", {})
                raise RuntimeError("Codex workspace resolution: " + json.dumps(content.get("error", {})))
            data = result["structuredContent"]["items"][0]["data"]
            if Path(data["root"]).resolve() != project.resolve():
                raise RuntimeError("Codex MCP used a stale or inherited directory instead of the current thread project.")
            context = request("mcpServer/tool/call", {"threadId": thread_id, "server": "sotea_workspace_test",
                                                     "tool": "sotea_context_get", "arguments": {}})
            expected_repo = "team/project-" + ("a" if project == projects[0] else "b")
            if context.get("isError") or context["structuredContent"]["items"][0]["data"]["repo"] != expected_repo:
                raise RuntimeError("The follow-workspace connection retained another project's repository default.")
            outside = projects[1] if project == projects[0] else projects[0]
            rejected = request("mcpServer/tool/call", {"threadId": thread_id, "server": "sotea_workspace_test",
                                                      "tool": "sotea_git_context", "arguments": {"workdir": str(outside)}})
            if not rejected.get("isError"):
                raise RuntimeError("Codex workspace access escaped into the other project.")
            results.append({"project": project.name, "status": "passed", "repository": expected_repo, "outside_project": "rejected"})
    finally:
        process.stdin.close()
        try:
            process.wait(timeout=15)
        except subprocess.TimeoutExpired:
            process.kill()
            process.wait()
        reader.join(timeout=3)
    return {"client": version, "status": "passed", "scenarios": results,
            "scope": "Actual app-server MCP transfer, no model turn or approval UI"}


def main():
    """执行隔离真实客户端验收并绑定实际产物摘要。

    只有完整成功才更新记录，不读取开发者凭证或更改真实客户端配置。
    """
    parser = argparse.ArgumentParser()
    parser.add_argument("--binary", type=Path, default=ROOT / "dist/p6-workspace/darwin-arm64/sotea")
    args = parser.parse_args()
    binary = args.binary.resolve()
    with tempfile.TemporaryDirectory(prefix="sotea-workspace-client-") as temporary:
        root = Path(temporary)
        codex_root = root / "codex"
        codex_root.mkdir()
        result = codex_workspace(binary, codex_root)
        claude = claude_workspace(binary, root)
    evidence = {"schema_version": 1, "time_utc": datetime.now(timezone.utc).isoformat(),
                "binary": str(binary.relative_to(ROOT)), "sha256": hashlib.sha256(binary.read_bytes()).hexdigest(),
                "codex": result, "claude": claude,
                "pending": ["Claude Code actual workspace tool calls", "Cursor and VS Code actual workspace calls", "Windows 10 / 11"]}
    (ROOT / "docs/validation/p6-workspace-clients-result.json").write_text(json.dumps(evidence, indent=2) + "\n")
    print("Codex: both project workspaces and cross-project rejection passed.")
    print("Claude Code: same user configuration launched both project environments; full tool UI remains pending.")


def claude_workspace(binary, root):
    """从两个项目启动实际 Claude Code，核对同一用户配置的项目环境传递。

    只运行 mcp list 健康检查；wrapper 记录目录来源，不读取凭证、不调用模型或业务工具。
    """
    executable = shutil.which("claude")
    if not executable:
        raise RuntimeError("The real Claude Code CLI is unavailable.")
    base = root / "claude"
    base.mkdir()
    home = base / "home"
    home.mkdir()
    environment = dict(os.environ, HOME=str(home), USERPROFILE=str(home), CLAUDE_CONFIG_DIR=str(base / "client"),
                       GIT_CONFIG_GLOBAL=os.devnull, GIT_CONFIG_NOSYSTEM="1")
    for name in ["SOTEA_TOKEN", "SOTEA_TOKEN_INSTANCE", "SOTEA_ACCOUNT", "CLAUDE_PROJECT_DIR", "ANTHROPIC_API_KEY", "CLAUDE_CODE_OAUTH_TOKEN"]:
        environment.pop(name, None)
    configuration = base / "sotea.json"
    configuration.write_text(json.dumps({"version": 1, "instances": {}, "repositories": []}))
    log = base / "launches.jsonl"
    wrapper = base / "launch.py"
    wrapper.write_text("import os,sys,json\n"
                       "with open(" + repr(str(log)) + ", 'a') as f:\n"
                       "    f.write(json.dumps({'project':os.environ.get('CLAUDE_PROJECT_DIR',''),'cwd':os.getcwd()})+'\\n')\n"
                       "os.execv(sys.argv[1],sys.argv[1:])\n")
    projects = [base / "project-a", base / "project b"]
    for project in projects:
        project.mkdir()
    server = {"type": "stdio", "command": sys.executable, "args": [str(wrapper), str(binary), "--config", str(configuration),
              "mcp", "serve", "--follow-workspace", "--workspace-source", "claude"]}
    added = subprocess.run([executable, "mcp", "add-json", "--scope", "user", "sotea_workspace_test", json.dumps(server)],
                           cwd=projects[0], env=environment, capture_output=True, text=True, timeout=30)
    if added.returncode != 0:
        raise RuntimeError("Claude Code could not save the isolated user configuration.")
    scenarios = []
    for project in projects:
        before = len(log.read_text().splitlines()) if log.exists() else 0
        checked = subprocess.run([executable, "mcp", "list"], cwd=project, env=environment,
                                 capture_output=True, text=True, timeout=30)
        if checked.returncode != 0 or not any("sotea_workspace_test" in line and "Connected" in line for line in checked.stdout.splitlines()):
            raise RuntimeError("Claude Code workspace health check did not connect.")
        launches = [json.loads(line) for line in log.read_text().splitlines()[before:]]
        if not launches or not all(item["project"] and Path(item["project"]).resolve() == project.resolve() for item in launches):
            raise RuntimeError("Claude Code did not supply the expected project environment to its MCP server.")
        scenarios.append({"project": project.name, "status": "passed", "project_environment": "matched", "health": "Connected"})
    return {"client": subprocess.check_output([executable, "--version"], text=True).strip(),
            "status": "partial", "scenarios": scenarios,
            "scope": "Actual user-scope launch and project environment, no model or business tool calls"}


if __name__ == "__main__":
    main()
