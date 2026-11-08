#!/usr/bin/env python3
"""以隔离用户目录和真实 POSIX PTY 验证四客户端配置向导。

不读取或改写开发者客户端配置；连接检查只调用本地 MCP 初始化和工具发现，不代表客户端 UI 验收。
"""
import argparse
from datetime import datetime, timezone
import hashlib
import importlib.util
import json
from pathlib import Path
import sys
import tempfile
import tomllib

ROOT = Path(__file__).resolve().parents[1]
SPEC = importlib.util.spec_from_file_location("p5_terminal", ROOT / "scripts/p5-terminal-smoke.py")
DRIVER = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(DRIVER)


def setup_steps(terminal, directory, write=False, raw=False, local=False, account="alice", repository="", follow=False):
    """按界面逐步选择已保存身份和独立权限。

    默认安装不选仓库或本地根；更新时可明确授予工作目录及写／raw 能力。
    """
    terminal.choose("MCP setup · Instance", "isolated")
    terminal.choose("MCP setup · Account", account)
    terminal.choose("MCP setup · Local access", "Follow client workspace" if follow else "Choose a fixed directory" if local else "Remote tools only")
    if local:
        terminal.wait("Absolute directory")
        terminal.send((str(directory) + "\r").encode())
    if not follow:
        terminal.choose("MCP setup · Optional repository", "Enter owner/repo" if repository else "No default repository")
        if repository:
            terminal.wait("MCP repository")
            terminal.send((repository + "\r").encode())
    if write:
        terminal.choose("MCP setup · Permissions", "Business writes")
    if raw:
        terminal.choose("" if write else "MCP setup · Permissions", "Raw API")
    terminal.choose("" if write or raw else "MCP setup · Permissions", "Continue with these permissions")
    terminal.wait("MCP setup · Executable")
    terminal.send(b"\r")
    terminal.wait("Review MCP configuration")
    terminal.send(b"\x1b")


def read_config(path, client):
    """从实际磁盘解析客户端配置用于语义检查。

    样本使用合法 JSON 或 TOML，无需导入产品内部解析实现。
    """
    if client == "Codex":
        return tomllib.loads(path.read_text())
    return json.loads(path.read_text())


def assert_entry(path, client, expected_extra, follow=False):
    """验证启动权限、原服务器与旁路托管记录都已实际落盘。

    不输出整份配置；测试中只有合成账号，任何 token 都不应进入客户端文件。
    """
    parsed = read_config(path, client)
    root = "mcp_servers" if client == "Codex" else "servers" if client == "VS Code" else "mcpServers"
    if parsed[root]["other"]["command"] != "fixture-other":
        raise RuntimeError("The wizard changed another MCP server.")
    args = parsed[root]["sotea"]["args"]
    flags = {"--allow-write", "--allow-raw-api", "--workdir"}
    if flags.intersection(args) != expected_extra:
        raise RuntimeError("The installed permission intent did not match the preview.")
    if ("--follow-workspace" in args) != follow:
        raise RuntimeError("The workspace-following mode did not match the preview.")
    if follow and ("--repo" in args or "--workdir" in args):
        raise RuntimeError("A following integration retained a fixed project default.")
    if "synthetic-p5-token" in path.read_text() or "env" in parsed[root]["sotea"]:
        raise RuntimeError("The client configuration exposed a credential.")
    if not Path(str(path) + ".sotea-mcp.json").exists():
        raise RuntimeError("The wizard did not save its ownership record.")


def scenario(binary, client, scope, follow=False):
    """完成取消、安装、连接检查、权限更新与定向移除。

    使用不可连接的回环端口，证明没有仓库或在线 Gitea 也能配置并验证本地协议服务。
    """
    with tempfile.TemporaryDirectory(prefix="sotea-mcp-integration-") as temporary:
        directory = Path(temporary)
        configuration = directory / "sotea.json"
        configuration.write_text(json.dumps({"version": 1, "instances": {"isolated": {
            "url": "http://127.0.0.1:1", "proxy": "direct", "active_account": "alice",
            "accounts": {name: {"source": "environment", "instance_url": "http://127.0.0.1:1"} for name in ["alice", "zara"]}
        }}, "repositories": []}))
        home = directory / "home"
        home.mkdir()
        if client == "Codex":
            path = (home if scope == "user" else directory) / ".codex/config.toml"
            original = "# Preserve this comment\nmodel = 'fixture'\n[mcp_servers.other]\ncommand = 'fixture-other'\n"
        else:
            relative = {"Claude Code": ".mcp.json", "Cursor": ".cursor/mcp.json", "VS Code": ".vscode/mcp.json"}[client]
            path = directory / relative
            if scope == "user":
                path = {"Claude Code": home / ".claude/.claude.json",
                        "Cursor": home / ".cursor/mcp.json",
                        "VS Code": home / ("Library/Application Support/Code/User/mcp.json" if sys.platform == "darwin" else ".config/Code/User/mcp.json")}[client]
            root = "servers" if client == "VS Code" else "mcpServers"
            original = json.dumps({root: {"other": {"command": "fixture-other"}}, "fixtureSetting": True})
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(original)
        terminal = DRIVER.Terminal(binary, directory, configuration, repository="", environment_extra={
            "HOME": str(home), "USERPROFILE": str(home), "CODEX_HOME": str(home / ".codex"),
            "CLAUDE_CONFIG_DIR": str(home / ".claude"), "APPDATA": str(home / "AppData/Roaming"),
            "XDG_CONFIG_HOME": str(home / ".config")})
        try:
            account = "zara" if client == "Codex" and scope == "project" else "alice"
            terminal.choose("Home ·", "MCP integrations")
            terminal.choose("Choose a client", client)
            terminal.choose("Scope", scope)
            if scope == "project":
                terminal.wait("Project directory")
                terminal.send(b"\r")
            terminal.wait("Client configuration file")
            terminal.send(b"\r")
            terminal.choose("MCP integrations · " + client + " · absent", "Set up integration")
            if client == "Codex" and scope == "project":
                setup_steps(terminal, directory, account=account, follow=follow)
                if path.read_text() != original:
                    raise RuntimeError("Preview wrote client settings before confirmation.")
                terminal.choose("Install MCP integration?", "Back")
                if path.read_text() != original or Path(str(path) + ".sotea-mcp.json").exists():
                    raise RuntimeError("Cancelling the installation wrote configuration.")
                titles = ["MCP setup · Executable", "MCP setup · Permissions"]
                if not follow:
                    titles.append("Optional repository")
                titles += ["MCP setup · Local access", "MCP setup · Account", "MCP setup · Instance"]
                for title in titles:
                    terminal.wait(title)
                    terminal.send(b"\x1b")
                terminal.choose("MCP integrations · " + client + " · absent", "Set up integration")
            setup_steps(terminal, directory, account=account, follow=follow)
            terminal.choose("Install MCP integration?", "Install integration")
            terminal.wait("MCP configuration saved")
            terminal.send(b"\x1b")
            assert_entry(path, client, set(), follow=follow)
            terminal.choose("MCP integrations · " + client + " · managed", "Check connection")
            terminal.wait("28 tools discovered" if follow else "27 tools discovered")
            terminal.send(b"\x1b")
            terminal.choose("MCP integrations · " + client + " · managed", "Update integration")
            setup_steps(terminal, directory, write=True, raw=True, local=True, account=account,
                        repository="team/repo" if client == "Codex" and scope == "project" else "")
            terminal.choose("Update MCP integration?", "Update integration")
            terminal.wait("MCP configuration saved")
            terminal.send(b"\x1b")
            assert_entry(path, client, {"--allow-write", "--allow-raw-api", "--workdir"})
            terminal.choose("MCP integrations · " + client + " · managed", "Check connection")
            terminal.wait("32 tools discovered")
            terminal.send(b"\x1b")
            if client == "Codex" and scope == "project":
                before = read_config(path, client)["mcp_servers"]["sotea"]
                terminal.choose("MCP integrations · Codex · managed", "Update integration")
                for title in ["MCP setup · Instance", "MCP setup · Account", "MCP setup · Local access", "Optional repository", "MCP setup · Permissions", "MCP setup · Executable"]:
                    terminal.wait(title)
                    terminal.send(b"\r")
                terminal.wait("Review MCP configuration")
                terminal.send(b"\x1b")
                terminal.choose("Update MCP integration?", "Update integration")
                terminal.wait("MCP configuration saved")
                terminal.send(b"\x1b")
                if read_config(path, client)["mcp_servers"]["sotea"] != before:
                    raise RuntimeError("Keeping current fields changed the account, repository or authorization root.")
            terminal.choose("MCP integrations · " + client + " · managed", "Remove integration")
            terminal.wait("Review MCP configuration")
            terminal.send(b"\x1b")
            terminal.choose("Remove MCP integration?", "Remove integration")
            terminal.wait("MCP configuration saved")
            terminal.send(b"\x1b")
            terminal.wait("MCP integrations · " + client + " · absent")
            parsed = read_config(path, client)
            root = "mcp_servers" if client == "Codex" else "servers" if client == "VS Code" else "mcpServers"
            if "sotea" in parsed[root] or parsed[root]["other"]["command"] != "fixture-other":
                raise RuntimeError("Removal changed an unrelated server or retained sotea.")
            if client == "Codex" and "# Preserve this comment" not in path.read_text():
                raise RuntimeError("TOML comments were lost.")
            terminal.choose("MCP integrations · " + client + " · absent", "Back to scope")
            terminal.choose("Scope", "Back to clients")
            terminal.choose("Choose a client", "Back to Home")
            terminal.choose("Home ·", "Quit")
            terminal.close()
        finally:
            terminal.abort()
    return {"client": client, "scope": scope, "status": "passed", "follow_workspace": follow, "discovered_tools": [28 if follow else 27, 32]}


def main():
    """对同一二进制执行四格式及用户／项目路径的闭环验证。

    证据只保存摘要和场景状态，不把临时配置、终端内容或凭证写入仓库。
    """
    parser = argparse.ArgumentParser()
    parser.add_argument("--binary", type=Path, default=ROOT / "dist/p6-integrations/darwin-arm64/sotea")
    parser.add_argument("--clients", nargs="+", choices=["Codex", "Claude Code", "Cursor", "VS Code"], default=["Codex", "Claude Code", "Cursor", "VS Code"])
    parser.add_argument("--scopes", nargs="+", choices=["project", "user"], default=["project", "user"])
    parser.add_argument("--follow", action="store_true")
    parser.add_argument("--result", type=Path, default=ROOT / "docs/validation/p6-integrations-result.json")
    args = parser.parse_args()
    binary = args.binary.resolve()
    results = []
    for scope in args.scopes:
        for client in args.clients:
            results.append(scenario(binary, client, scope, follow=args.follow))
            print(client + " " + scope + ": passed", flush=True)
    evidence = {"schema_version": 1, "time_utc": datetime.now(timezone.utc).isoformat(),
                "binary": str(binary.relative_to(ROOT)), "sha256": hashlib.sha256(binary.read_bytes()).hexdigest(),
                "scope": "Isolated POSIX PTY and real MCP stdio; no client UI or real Gitea account",
                "scenarios": results, "pending": ["iTerm2 / Terminal GUI", "Windows 10 / 11", "Four client configuration reload and approval UI"]}
    args.result.write_text(json.dumps(evidence, indent=2) + "\n")


if __name__ == "__main__":
    main()
