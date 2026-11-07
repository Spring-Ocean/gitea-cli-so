#!/usr/bin/env python3
"""以隔离账户 API 和真实 PTY 验证账户／组织浏览及仓库工作台返回。

只使用合成 token 和临时配置，不读取开发者凭证；所有 HTTP 写方法均记录为失败。
"""
import argparse
from datetime import datetime, timezone
import hashlib
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import importlib.util
import json
import os
from pathlib import Path
import re
import shlex
import shutil
import subprocess
import tempfile
import threading
import urllib.parse

ROOT = Path(__file__).resolve().parents[1]
SPEC = importlib.util.spec_from_file_location("p5_terminal", ROOT / "scripts/p5-terminal-smoke.py")
TERMINAL = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(TERMINAL)


def repository(number, name):
    """构造带稳定 ID 的合成仓库。

    不把用户位置、Git HEAD 或真实组织内容引入候选。
    """
    return {"id": number, "name": name.split("/")[-1], "full_name": name, "private": True,
            "description": "Browser fixture", "default_branch": "main", "owner": {"login": name.split("/")[0]}}


def serve():
    """建立受控的分页、组织和仓库 API。

    查询全部要求测试身份；写请求被拒绝并计数，以保护浏览只读契约。
    """
    state = {"writes": 0, "queries": [], "lock": threading.RLock()}
    personal = [repository(index, f"alice/project-{index}") for index in range(1, 32)]
    organization = {"id": 77, "name": "team-org", "username": "team-org", "full_name": "Team Organization"}
    org_repository = repository(701, "team-org/project")

    class Handler(BaseHTTPRequestHandler):
        """在本地固定 API 范围回复 JSON。

        访问日志禁用，测试输出不包含请求认证头。
        """
        def reply(self, value, status=200):
            """发送有限 JSON 响应及明确状态。

            断开的取消请求不影响测试服务清理。
            """
            self.send_response(status)
            self.end_headers()
            try:
                self.wfile.write(json.dumps(value).encode())
            except (BrokenPipeError, ConnectionResetError):
                pass

        def do_GET(self):
            """提供账户仓库、组织成员与组织仓库的实际分页行为。

            详情核对完整名称；未知路径不能以空结果伪装成功。
            """
            address = urllib.parse.urlsplit(self.path)
            path = urllib.parse.unquote(address.path)
            query = urllib.parse.parse_qs(address.query)
            if path == "/api/v1/version":
                return self.reply({"version": "1.27.3"})
            with state["lock"]:
                state["queries"].append((path, query))
            if self.headers.get("Authorization") != "token synthetic-p5-token":
                return self.reply({"message": "sign in required"}, 401)
            if path == "/api/v1/user":
                return self.reply({"login": "alice", "full_name": "Alice Browser"})
            if path == "/api/v1/user/repos":
                page, limit = int(query["page"][0]), int(query["limit"][0])
                return self.reply(personal[(page-1)*limit:page*limit])
            if path == "/api/v1/user/orgs":
                return self.reply([organization])
            if path == "/api/v1/orgs/team-org":
                return self.reply(organization)
            if path == "/api/v1/orgs/team-org/repos":
                return self.reply([org_repository])
            if path == "/api/v1/repos/team/root":
                return self.reply(repository(999, "team/root"))
            if path == "/api/v1/repos/team-org/project":
                return self.reply(org_repository)
            for value in personal:
                if path == "/api/v1/repos/" + value["full_name"]:
                    return self.reply(value)
            return self.reply({"message": "unknown fixture path"}, 404)

        def do_POST(self):
            """拒绝任何浏览期间的写入请求并记录。

            PATCH／PUT／DELETE 复用相同检测，不能通过其他方法绕过断言。
            """
            with state["lock"]:
                state["writes"] += 1
            self.reply({"message": "writes forbidden in browser test"}, 405)

        do_PATCH = do_POST
        do_PUT = do_POST
        do_DELETE = do_POST

        def log_message(self, *args):
            """禁止把请求信息写入测试终端。

            结果仅保留通过的场景和二进制摘要。
            """
            return

    server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    return server, thread, state


def back(terminal, marker):
    """通过一次明确的返回操作离开当前层。

    等待目标页渲染，避免连续 Esc 错误取消后台读取。
    """
    terminal.send(b"q\r" if terminal.plain else b"\x1b")
    terminal.wait(marker)


def plain_output(terminal):
    """移除终端控制序列以便验证恢复后的上下文。

    断言只检查可观察字段，不依赖颜色和完整布局快照。
    """
    return re.sub(r"\x1b\[[0-?]*[ -/]*[@-~]", "", terminal.output.decode(errors="replace"))


def count_requests(state, path):
    """读取受控服务记录的指定 API 请求数。

    锁与 ThreadingHTTPServer 共用，计数只按端点路径统计。
    """
    with state["lock"]:
        return sum(1 for requested, _ in state["queries"] if requested == path)


def restore_original_context(terminal, marker):
    """逐层退出失效浏览工作台，直到原仓库回到首页。

    每次按键后检查当前屏幕，并对循环次数设上限防止异常菜单卡住。
    """
    for _ in range(8):
        terminal.read(0.2)
        visible = plain_output(terminal)
        if marker in visible and "Home ·" in visible:
            return
        terminal.send(b"q\r" if terminal.plain else b"\x1b")
    terminal.wait(marker)


def scenario(binary, configuration, directory, mode):
    """走完个人分页、组织仓库和工作台，再核对配置未被浏览修改。

    VT 还验证远端工作台不能调用原目录 Git，并保持完整终端恢复。
    """
    original = configuration.read_bytes()
    real_git = shutil.which("git")
    if not real_git:
        raise RuntimeError("Git is required to prepare and verify the browser smoke fixture.")
    wrapper_directory = directory / "browser-test-bin"
    wrapper_directory.mkdir(exist_ok=True)
    git_log = directory / "browser-git-invocations.log"
    git_wrapper = wrapper_directory / "git"
    git_wrapper.write_text(
        "#!/bin/sh\n"
        "printf 'called\\n' >> \"$SOTEA_P5_GIT_LOG\"\n"
        f"exec {shlex.quote(real_git)} \"$@\"\n"
    )
    git_wrapper.chmod(0o700)
    environment_extra = {
        "PATH": str(wrapper_directory) + os.pathsep + os.environ.get("PATH", ""),
        "SOTEA_P5_GIT_LOG": str(git_log),
    }
    terminal = TERMINAL.Terminal(binary, directory, configuration, term=mode, repository="team/root",
                                 environment_extra=environment_extra)
    try:
        terminal.wait("Home ·")
        terminal.choose("Home ·", "Browse account")
        terminal.choose("Browse account ·", "repositories")
        terminal.wait("Account repositories · Page 1")
        terminal.choose("Account repositories · Page 1", "Next page")
        terminal.wait("Account repositories · Page 2")
        terminal.choose("Account repositories · Page 2", "Filter this page")
        terminal.wait("Filter this page by name")
        terminal.send(b"project-31\r")
        terminal.wait("Account repositories · Page 2")
        terminal.choose("Account repositories · Page 2", "alice/project-31")
        terminal.wait("Home ·")
        terminal.wait("alice/project-31")
        git_log.write_text("")
        if not terminal.plain:
            terminal.wait("No local checkout")
            terminal.send(b"/")
            terminal.wait("Search:")
            terminal.send(b"literal arguments\r")
            terminal.wait("No matching actions")
            terminal.send(b"/")
            terminal.send(b"\x7f" * len("literal arguments") + b"\r")
        terminal.choose("Repository", "repo")
        terminal.choose("sotea repo", "repo view")
        terminal.group("Overview")
        terminal.wait("alice/project-31")
        if not terminal.plain:
            back(terminal, "sotea repo")
        if git_log.exists() and git_log.read_text():
            raise RuntimeError("The remote workspace invoked local Git.")
        back(terminal, "Home ·")
        terminal.choose("Home ·", "Back to repository list")
        terminal.wait("Account repositories · Page 2")
        terminal.wait("project-31")
        back(terminal, "Browse account ·")
        terminal.choose("Browse account ·", "organizations")
        terminal.choose("Organizations · Page 1", "team-org")
        terminal.choose("Organization repositories · team-org", "team-org/project")
        terminal.wait("Home ·")
        terminal.wait("team-org/project")
        terminal.choose("Home ·", "Back to repository list")
        terminal.wait("Organization repositories · team-org")
        back(terminal, "Organizations · Page 1")
        back(terminal, "Browse account ·")
        terminal.choose("Browse account ·", "Back to Home")
        terminal.wait("Home ·")
        terminal.wait("team/root")
        terminal.choose("Home ·", "Quit")
        terminal.close()
        if configuration.read_bytes() != original:
            raise RuntimeError("Account browsing changed persistent configuration.")
    finally:
        terminal.abort()


def scenario_stale_workspace(binary, configuration, directory, mode, state):
    """外部配置修订后拒绝旧浏览工作台中的下一次仓库读取。

    核对旧仓库 API 请求数不增加，并验证退出后首页恢复启动仓库上下文。
    """
    isolated_configuration = directory / f"stale-workspace-{mode}.json"
    isolated_configuration.write_bytes(configuration.read_bytes())
    terminal = TERMINAL.Terminal(binary, directory, isolated_configuration, term=mode,
                                 repository="team/root")
    try:
        terminal.wait("Home ·")
        terminal.choose("Home ·", "Browse account")
        terminal.choose("Browse account ·", "repositories")
        terminal.choose("Account repositories · Page 1", "alice/project-1")
        terminal.wait("Home ·")
        terminal.wait("alice/project-1")
        request_path = "/api/v1/repos/alice/project-1"
        before = count_requests(state, request_path)
        if before == 0:
            raise RuntimeError("The selected remote workspace was not verified before the stale-context test.")

        updated = json.loads(isolated_configuration.read_text())
        updated["revision"] += 1
        isolated_configuration.write_text(json.dumps(updated))
        changed = isolated_configuration.read_bytes()

        terminal.action("repo", "view")
        terminal.wait("The account context changed")
        after = count_requests(state, request_path)
        if after != before:
            raise RuntimeError("A stale repository view reached the Gitea API after configuration changed.")

        restore_original_context(terminal, "team/root")
        if isolated_configuration.read_bytes() != changed:
            raise RuntimeError("Returning from a stale workspace overwrote the external configuration change.")
        terminal.choose("Home ·", "Quit")
        terminal.close()
    finally:
        terminal.abort()


def main():
    """以同一候选产物执行 VT 和静态浏览回归并保存摘要。

    本记录是 POSIX PTY 证据，不代表 Windows 或 iTerm2 GUI 验收。
    """
    parser = argparse.ArgumentParser()
    parser.add_argument("--binary", type=Path, default=ROOT / "dist/p5/darwin-arm64/sotea")
    binary = parser.parse_args().binary.resolve()
    server, thread, state = serve()
    try:
        with tempfile.TemporaryDirectory(prefix="sotea-browser-") as temporary:
            directory = Path(temporary)
            subprocess.run(["git", "init", "--quiet", str(directory)], check=True)
            url = f"http://127.0.0.1:{server.server_port}"
            configuration = directory / "config.json"
            configuration.write_text(json.dumps({"version": 1, "revision": 1, "instances": {"isolated": {
                "url": url, "proxy": "direct", "active_account": "alice", "accounts": {
                    "alice": {"source": "environment", "instance_url": url}}}}, "repositories": []}))
            for mode in ("xterm-256color", "dumb"):
                scenario(binary, configuration, directory, mode)
                print(f"P5 {mode} account and organization browser passed.", flush=True)
                scenario_stale_workspace(binary, configuration, directory, mode, state)
                print(f"P5 {mode} stale workspace isolation passed.", flush=True)
            if state["writes"]:
                raise RuntimeError("A directory operation performed an HTTP write.")
        record = {"schema_version": 1, "time_utc": datetime.now(timezone.utc).isoformat(),
                  "status": "passed", "binary_sha256": hashlib.sha256(binary.read_bytes()).hexdigest(),
                  "checks": ["account_repository_pages", "page_filter_retained_after_workspace",
                             "organization_repository_navigation", "remote_workspace_no_git_passthrough",
                             "workspace_context_restored", "stale_workspace_view_blocked_after_config_change",
                             "original_context_restored_after_stale_workspace", "no_configuration_write",
                             "zero_http_writes", "terminal_restored"]}
        (ROOT / "tests/integration/.state/p5-browser-result.json").write_text(json.dumps(record, indent=2) + "\n")
    finally:
        server.shutdown()
        server.server_close()
        thread.join(timeout=2)


if __name__ == "__main__":
    main()
