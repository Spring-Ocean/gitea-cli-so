#!/usr/bin/env python3
"""通过真实 POSIX PTY 验证持续菜单、共享表单和终端交接。

合成服务只监听本机随机端口；真实宿主与 Windows 验收必须另行记录。
"""
import argparse
from datetime import datetime, timezone
import fcntl
import hashlib
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import os
from pathlib import Path
import pty
import re
import select
import signal
import socket
import struct
import subprocess
import tempfile
import termios
import threading
import time
import urllib.parse

ROOT = Path(__file__).resolve().parents[1]


class Terminal:
    """拥有三流 PTY 与子进程，以真实按键驱动界面。

    每次关闭核对终端模式并回收进程，输出只在内存用于断言。
    """
    def __init__(self, binary, directory, configuration, term="xterm-256color", args=(), environment_extra=None, repository="team/repo", instance="isolated", columns=140, color="never"):
        """启动隔离命令，默认无参数进入持续菜单。

        环境只提供合成 token，禁用开发者 Git 配置和 CI。
        """
        self.master, self.slave = pty.openpty()
        fcntl.ioctl(self.slave, termios.TIOCSWINSZ, struct.pack("HHHH", 32, columns, 0, 0))
        self.original = termios.tcgetattr(self.slave)
        self.plain = term == "dumb"
        environment = dict(os.environ, TERM=term, NO_COLOR="1", SOTEA_UPDATE_CHECK="never", SOTEA_TOKEN="synthetic-p5-token",
                           SOTEA_TOKEN_INSTANCE="isolated", GIT_CONFIG_NOSYSTEM="1", GIT_CONFIG_GLOBAL=os.devnull)
        for name in ("CI", "SOTEA_INSTANCE", "SOTEA_ACCOUNT", "SOTEA_REPO", "SOTEA_CONFIG"):
            environment.pop(name, None)
        environment.update(environment_extra or {})
        context_args = (["--instance", instance] if instance else []) + (["--repo", repository] if repository else [])
        self.process = subprocess.Popen([str(binary), "--config", str(configuration), "--workdir", str(directory),
                                         *context_args, "--color", color, *args],
                                        env=environment, stdin=self.slave, stdout=self.slave, stderr=self.slave,
                                        start_new_session=True)
        self.closed = False
        self.output = bytearray()
        self.all_output = bytearray()
        self.configuration_notice = False

    def read(self, duration=0.15):
        """有界读取实际终端输出，保留短时渲染增量。

        不把完整屏幕写入证据，以免远端业务内容或合成凭证泄露。
        """
        deadline = time.monotonic() + duration
        while time.monotonic() < deadline:
            if select.select([self.master], [], [], 0.02)[0]:
                chunk = os.read(self.master, 65536)
                self.output.extend(chunk)
                self.all_output.extend(chunk)
                self.configuration_notice = b"Configuration changed" in self.all_output

    def wait(self, marker):
        """等待可见语义标记，不断言样式快照。

        超时只给出最后的安全屏幕片段，不包含认证数据。
        """
        if not self.plain and marker.endswith(" · exit 0"):
            path = marker.removesuffix(" · exit 0").split()
            names = {"repo": "Repository", "issue": "Issues", "pr": "Pull requests",
                     "release": "Releases", "tag": "Tags", "git": "Git",
                     "api": "API", "version": "Version"}
            marker = "Home › " + " › ".join([names.get(path[0], path[0].capitalize()),
                                                     *[part.capitalize() for part in path[1:]]])
        deadline = time.monotonic() + 12
        scrolled = False
        while marker not in re.sub(r"\x1b\[[0-?]*[ -/]*[@-~]", "", self.output.decode(errors="replace")):
            if not scrolled and not self.plain and " · exit " in marker and b"Esc Back" in self.output and b"Lines " in self.output:
                self.send(b"\x1b[F")
                scrolled = True
                continue
            if self.process.poll() is not None or time.monotonic() > deadline:
                plain = re.sub(r"\x1b\[[0-?]*[ -/]*[@-~]", "", (self.output or self.all_output[-4000:]).decode(errors="replace"))
                plain = "".join(c for c in plain if c.isprintable() or c in "\r\n")
                raise RuntimeError(f"PTY did not reach {marker!r}; exit={self.process.poll()}; screen={plain[-1400:]!r}")
            self.read()

    def send(self, value):
        """发送固定场景按键，丢弃旧的等待缓存。

        小量等待让单次渲染稳定，避免将增量输出误认为完整重绘。
        """
        self.output.clear()
        os.write(self.master, value)
        self.read()

    def group(self, marker):
        """在结果页通过 Tab 浏览完整分组，不用左右键。

        通过活动标签而非所有可见标签判断当前组，避免把未激活的名称误当正文。
        """
        if self.plain:
            self.wait(marker)
            return
        self.read(0.3)
        active = "› " + marker
        for _ in range(20):
            visible = re.sub(r"\x1b\[[0-?]*[ -/]*[@-~]", "", self.output.decode(errors="replace"))
            if active in visible:
                return
            self.send(b"\t")
        self.wait(active)

    def choose(self, title, query, multi=False):
        """导航与单资源用 Enter 接受焦点，只有批量资源使用 Space。

        按语义区分资源查询与本地导航，不依赖完整样式快照。
        """
        self.wait(title)
        if self.plain:
            self.send(("/" + query + "\r").encode())
            self.wait("Number")
            self.send(b"1\r")
            if title.startswith("Select ") and ("close" in getattr(self, "active_path", "") or "reopen" in getattr(self, "active_path", "")):
                self.wait("Selected 1")
                if multi:
                    self.send(b"2\r")
                    self.wait("Selected 2")
                self.send(b"done\r")
        else:
            resource = title.startswith("Select ") or title == "Repository candidates"
            # 固定页脚可被渲染器复用，搜索后的增量不一定重复Space提示。
            batch = resource and (b"Space Select" in self.output or
                                  any(getattr(self, "active_path", "").endswith(" " + action)
                                      for action in ("close", "reopen")))
            self.send(b"/")
            self.wait("Search candidates:" if resource else "Search:")
            self.send((query + "\r").encode())
            self.wait("Search: " + query)
            self.read()
            if batch:
                self.send(b" ")
                self.wait("Selected 1")
                if multi:
                    self.send(b"\x1b[B ")
                    self.wait("Selected 2")
            self.send(b"\r")

    def home(self):
        """逐层返回主页，结果页和嵌套菜单都不能关闭持续会话。

        每步等待渲染，最多八层防止测试误入无限循环。
        """
        self.read()
        for _ in range(8):
            if "Home ·" in self.output.decode(errors="replace"):
                return
            self.send(b"q\r" if self.plain else b"\x1b")
            self.read(0.4)
            if not self.plain and b"Checking Gitea and repository" in self.output:
                deadline = time.monotonic() + 12
                while b"Home " not in self.output and b"Context update" not in self.output:
                    if time.monotonic() > deadline:
                        raise RuntimeError("Repository refresh did not finish.")
                    self.read()
                if b"Context update" in self.output:
                    self.send(b"\x1b")
                    self.wait("Home ·")
        raise RuntimeError("Esc did not return to Home.")

    def action(self, group, leaf, options=False):
        """从主页按实际树进入动作，只读命令无需先经过字段表。

        options 明确进入可选配置页，稳定命令路径仍可用于搜索。
        """
        self.home()
        self.choose("Home ·", group)
        parts = leaf.split()
        parent = group
        for part in parts[:-1]:
            self.choose("sotea " + parent, parent + " " + part)
            parent += " " + part
        if options:
            self.choose("sotea " + parent, "Run with options")
            self.choose("Choose an action", parent + " " + parts[-1])
        else:
            self.choose("sotea " + parent, parent + " " + parts[-1])
        self.active_path = group + " " + leaf

    def fields_done(self, path):
        """明确接受当前参数集合，不让菜单替用户选择目标。

        表单没有输入时继续进入原有缺参路径。
        """
        self.choose("Fields · sotea " + path, "Continue with current fields")

    def wait_exit(self, timeout):
        """等待退出时持续排空 PTY，避免测试自身将输出堵在终端缓冲区。

        这是进程生命周期等待，不依赖固定屏幕大小或完整样式快照。
        """
        deadline = time.monotonic() + timeout
        while self.process.poll() is None:
            if time.monotonic() > deadline:
                raise RuntimeError("PTY process did not exit before the deadline.")
            self.read(0.03)
        self.read()

    def close(self, expected=0):
        """有界等待进程退出并核对真实终端已恢复。

        Darwin 的瞬态 PENDIN 不属于输入模式破坏，其他状态均须相同。
        """
        try:
            self.wait_exit(5)
            self.read()
            if self.process.returncode != expected:
                raise RuntimeError(f"Unexpected PTY exit: {self.process.returncode}, expected {expected}.")
            restored = termios.tcgetattr(self.slave)
            mask = getattr(termios, "PENDIN", 0)
            restored[3] &= ~mask
            original = list(self.original)
            original[3] &= ~mask
            if restored != original:
                raise RuntimeError("Terminal input mode was not restored.")
            if b"synthetic-p5-token" in self.all_output:
                raise RuntimeError("A synthetic token reached the terminal.")
        finally:
            self.abort()

    def abort(self):
        """回收当前 PTY 和未退出的子进程，允许重复调用。

        异常路径与模式验证共用同一资源所有权，避免重复关闭掩盖原始错误。
        """
        if self.closed:
            return
        if self.process.poll() is None:
            self.process.kill()
            self.process.wait()
        os.close(self.master)
        os.close(self.slave)
        self.closed = True


class Fixture:
    """保存可观察写入和完整资源状态，供真实 PTY 断言。

    每个场景独立实例，线程访问都持锁。
    """
    def __init__(self):
        """建立两个 Issue、一个 PR 及现有 Git 标签。

        标题含中文、Emoji 和恶意控制符，用于展示边界。
        """
        self.lock = threading.RLock()
        self.writes = []
        self.issues = {number: {"id": number, "number": number, "title": f"p5 issue {number} 中文👩🏽‍💻",
                                "body": "original body", "state": "open", "assignees": [], "labels": [],
                                "updated_at": "initial", "milestone": None} for number in (1, 2)}
        self.pr = {"id": 3, "number": 3, "title": "p5 PR", "state": "open", "body": "", "assignees": [], "labels": [],
                   "head": {"ref": "feature", "sha": "a"*40}, "base": {"ref": "main"}, "user": {"login": "bob"}}
        self.release = None
        self.disconnect_next = False
        self.delay = None
        self.started = threading.Event()


def serve(fixture):
    """启动仅包含场景所需端点的可控服务。

    未知路径明确报错，不能靠空成功掩盖命令调用了错误端点。
    """
    class Handler(BaseHTTPRequestHandler):
        """以固定 API 边界读写夹具。

        禁止访问日志输出认证头，失败状态保留真实语义。
        """
        def reply(self, value, status=200):
            """发送单个完整 JSON 响应。

            错误也有有限响应体，调用端必须按状态处理。
            """
            self.send_response(status)
            self.end_headers()
            try:
                self.wfile.write(json.dumps(value).encode())
            except (BrokenPipeError, ConnectionResetError):
                return

        def do_GET(self):
            """提供身份、版本、分页和前置状态查询。

            所有资源返回独立序列化结果，不将线程间可变对象暴露给调用方。
            """
            path = urllib.parse.unquote(urllib.parse.urlsplit(self.path).path)
            delay = fixture.delay
            if path.endswith("/issues") and delay is not None:
                fixture.started.set()
                delay.wait(10)
            with fixture.lock:
                if path.endswith("/version"):
                    self.reply({"version": "28.3.0"})
                elif path.endswith("/display-sample"):
                    self.reply([{"id": 9007199254740993, "future_value": {"enabled": False, "token": "synthetic-json-secret"},
                                 "body": "First line\nSecond line"}])
                elif path.endswith("/long-display-sample"):
                    self.reply({"body": "\n".join(f"Body line {number}" for number in range(1, 61)),
                                "tail": {"final": "Complete tail group"}})
                elif path.endswith("/plain-sample"):
                    self.send_response(200)
                    self.end_headers()
                    self.wfile.write(b"RAW MENU OUTPUT\n")
                elif path.endswith("/user"):
                    self.reply({"login": "alice", "full_name": "Alice Ocean", "email": "alice@example.invalid"})
                elif path.endswith("/repos/team/repo"):
                    self.reply({"full_name": "team/repo", "description": "A shared project", "private": True, "default_branch": "main", "clone_url": "https://git.example/team/repo.git", "stars_count": 12})
                elif path.endswith("/issues"):
                    query = urllib.parse.parse_qs(urllib.parse.urlsplit(self.path).query)
                    state = query.get("state", ["all"])[0]
                    self.reply([item for item in fixture.issues.values() if state == "all" or item["state"] == state])
                elif "/issues/" in path:
                    self.reply(fixture.issues[int(path.rsplit("/", 1)[1])])
                elif path.endswith("/pulls"):
                    self.reply([fixture.pr])
                elif "/git/commits/" in path:
                    self.reply({"sha": "a"*40})
                elif path.endswith("/pulls/3"):
                    self.reply(fixture.pr)
                elif path.endswith("/releases"):
                    self.reply([fixture.release] if fixture.release else [])
                elif "/releases/tags/" in path:
                    self.reply(fixture.release or {"message": "not found"}, 200 if fixture.release else 404)
                elif path.endswith("/tags"):
                    self.reply([{"name": "v-p5", "commit": {"sha": "a"*40}}])
                elif "/tags/" in path:
                    self.reply({"name": "v-p5", "commit": {"sha": "a"*40}})
                elif path.endswith("/labels") or path.endswith("/milestones"):
                    self.reply([])
                else:
                    self.reply({"message": "unknown fixture path"}, 404)

        def do_PATCH(self):
            """记录且只应用明确的 Issue 变更。

            回读使用同一状态，使 CLI 的写后核对能检测错误字段。
            """
            number = int(urllib.parse.urlsplit(self.path).path.rsplit("/", 1)[1])
            payload = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
            with fixture.lock:
                fixture.writes.append(("issue", number, payload))
                fixture.issues[number].update(payload)
                if fixture.disconnect_next:
                    fixture.disconnect_next = False
                    self.connection.shutdown(socket.SHUT_RDWR)
                    self.connection.close()
                    return
                self.reply(fixture.issues[number])

        def do_POST(self):
            """区分 PR 审查和 Release 创建，不将讨论冒充审查。

            返回字段满足写后核对；重复创建不会被此夹具隐式覆盖。
            """
            path = urllib.parse.urlsplit(self.path).path
            payload = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
            with fixture.lock:
                fixture.writes.append(("post", path, payload))
                if path.endswith("/pulls/3/reviews"):
                    self.reply({"id": 1, "state": payload["event"], "body": payload["body"], "commit_id": payload["commit_id"]})
                elif path.endswith("/releases") and fixture.release is None:
                    fixture.release = {"id": 1, **payload}
                    self.reply(fixture.release, 201)
                else:
                    self.reply({"message": "unexpected write"}, 409)

        def log_message(self, *args):
            """禁用默认 HTTP 访问日志。

            测试记录仅保留检查名称及产物摘要。
            """
            return

    server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    return server, thread


def session(binary, directory, config, fixture, term):
    """验证层级导航、屏内结果、写入边界、未知结果及 Git 交接。

    静态降级保留同一业务风险覆盖；VT 额外核对唯一全屏生命周期。
    """
    terminal = Terminal(binary, directory, config, term)
    try:
        terminal.wait("Home ·")
        if not terminal.plain:
            terminal.wait("Alice Ocean")
            terminal.wait("alice@example.invalid")
        terminal.action("repo", "view")
        terminal.group("Overview")
        terminal.wait("A shared project")
        terminal.group("Links")
        terminal.action("tag", "list")
        terminal.wait("tag list · exit 0")
        if not terminal.plain:
            terminal.wait("Esc Back · Enter Back")
            raw = bytes(terminal.all_output)
            if raw.count(b"\x1b[?1049h") != 1 or b"\x1b[?1049l" in raw:
                raise RuntimeError("A normal result left the shared alternate screen.")
            terminal.send(b"\x1b")
            terminal.wait("sotea tag")

        terminal.action("issue", "close")
        terminal.choose("Select issues", "p5 issue")
        terminal.wait("[y/N]")
        if fixture.writes:
            raise RuntimeError("A write occurred before confirmation.")
        terminal.send(b"\r")
        terminal.wait("issue close · exit 130")
        if fixture.writes:
            raise RuntimeError("Default No performed a write.")

        terminal.action("issue", "edit")
        terminal.choose("Select issues", "p5 issue")
        terminal.fields_done("issue edit")
        terminal.wait("No changes selected")
        if fixture.writes:
            raise RuntimeError("An unchanged form performed a write.")

        terminal.action("issue", "edit")
        terminal.choose("Select issues", "p5 issue")
        terminal.choose("Fields ·", "--clear-body")
        terminal.choose("--clear-body", "Set true")
        terminal.fields_done("issue edit")
        terminal.wait("[y/N]")
        terminal.send(b"y\r")
        terminal.wait("issue edit · exit 0")
        if fixture.issues[1]["body"] != "" or fixture.issues[1]["title"] != "p5 issue 1 中文👩🏽‍💻":
            raise RuntimeError("Explicit clear or omitted-field preservation failed.")

        terminal.action("issue", "close")
        terminal.choose("Select issues", "p5 issue", multi=True)
        terminal.wait("[y/N]")
        terminal.send(b"y\r")
        terminal.wait("issue close · exit 0")
        terminal.home()
        terminal.choose("Home ·", "Last business result")
        terminal.wait("success" if terminal.plain else "Home › Issues › Close")

        with fixture.lock:
            fixture.disconnect_next = True
            before = len(fixture.writes)
        terminal.action("issue", "reopen")
        terminal.choose("Select issues", "p5 issue")
        terminal.wait("[y/N]")
        terminal.send(b"y\r")
        terminal.wait("issue reopen · exit 4")
        terminal.home()
        terminal.choose("Home ·", "Last business result")
        terminal.wait("unknown")
        if len(fixture.writes) != before + 1 or fixture.issues[1]["state"] != "open":
            raise RuntimeError("An unknown write was replayed or lost its actual effect.")

        terminal.action("pr", "review create")
        terminal.fields_done("pr review create")
        terminal.choose("Select PRs", "p5 PR")
        terminal.choose("Review event", "Approve")
        terminal.wait("[y/N]")
        terminal.send(b"y\r")
        terminal.wait("pr review create · exit 0")

        terminal.action("release", "create")
        terminal.fields_done("release create")
        terminal.choose("Release tag", "Choose an existing")
        terminal.choose("Repository candidates", "v-p5")
        terminal.wait("[y/N]")
        terminal.send(b"y\r")
        terminal.wait("release create · exit 0")
        if not fixture.release or fixture.release.get("draft") is not True:
            raise RuntimeError("Release creation lost its draft default.")

        terminal.home()
        terminal.choose("Home ·", "api")
        terminal.fields_done("api")
        terminal.wait("API path (relative")
        terminal.send(b"/version\r")
        terminal.wait("api · exit 0")
        if terminal.plain:
            if b'"version": "28.3.0"' not in terminal.all_output:
                raise RuntimeError("The static API response was not handed to the real terminal.")
        else:
            terminal.wait("Version")
            terminal.wait("28.3.0")
            if b'"version":' in terminal.output:
                raise RuntimeError("The menu dumped JSON instead of readable API fields.")
            terminal.home()
            terminal.choose("Home ·", "api")
            terminal.fields_done("api")
            terminal.wait("API path (relative")
            terminal.send(b"/display-sample\r")
            terminal.wait("api · exit 0")
            terminal.wait("9007199254740993")
            terminal.wait("Future value")
            terminal.wait("[REDACTED]")
            if b"synthetic-json-secret" in terminal.all_output:
                raise RuntimeError("Readable API details exposed a secret field.")
            terminal.home()
            terminal.choose("Home ·", "api")
            terminal.fields_done("api")
            terminal.wait("API path (relative")
            terminal.send(b"/plain-sample\r")
            terminal.wait("api · exit 0")
            if b"RAW MENU OUTPUT" not in terminal.all_output:
                raise RuntimeError("Non-JSON API output lost its raw terminal handoff.")

        for arguments, expected in ((["status", "--short", "--", " file with space "], "git · exit 0"),
                                    (["not-a-sotea-git-command"], "git · exit 1")):
            terminal.home()
            terminal.choose("Home ·", "git")
            for argument in arguments:
                terminal.choose("Git arguments:", "Add one")
                terminal.wait("One literal Git argument")
                if argument == " file with space ":
                    (directory / argument).write_text("p5")
                terminal.send((argument + "\r").encode())
            terminal.choose("Git arguments:", "Done")
            terminal.wait(expected)
        if b'?? " file with space "' not in re.sub(rb"\x1b\[[0-?]*[ -/]*[@-~]", b"", bytes(terminal.all_output)):
            raise RuntimeError("Git did not receive the literal whitespace argument.")

        terminal.home()
        changed = json.loads(config.read_text())
        changed["revision"] += 1
        config.write_text(json.dumps(changed))
        terminal.choose("Home ·", "version")
        terminal.wait("version · exit 0")
        terminal.home()
        if not terminal.configuration_notice:
            raise RuntimeError("An external configuration change was not reported.")

        terminal.choose("Home ·", "Save defaults")
        terminal.wait("Save these exact defaults [y/N]")
        terminal.send(b"y\r")
        terminal.home()
        if json.loads(config.read_text()).get("default_instance") != "isolated":
            raise RuntimeError("Explicit default save failed.")

        terminal.choose("Home ·", "Save defaults")
        terminal.wait("Save these exact defaults [y/N]")
        concurrent = json.loads(config.read_text())
        concurrent["revision"] += 1
        config.write_text(json.dumps(concurrent))
        terminal.send(b"y\r")
        terminal.wait("CONFIG_CONFLICT")
        if json.loads(config.read_text())["revision"] != concurrent["revision"]:
            raise RuntimeError("Default save overwrote a concurrent edit.")

        terminal.home()
        terminal.choose("Home ·", "Quit")
        terminal.close()
        if not terminal.plain and bytes(terminal.all_output).count(b"\x1b[?1049l") != 4:
            # JSON API 留屏；纯文本 API、Git 两次交接和最终退出。
            raise RuntimeError("Unexpected full-screen lifecycle outside explicit Git handoffs.")
    finally:
        terminal.abort()


def entry_contract(binary):
    """验证任一流重定向及 CI／JSON／help／no-interactive 的真实终端门槛。

    每例独立进程且不提供用户输入，必须有界退出，不能意外进入菜单。
    """
    samples = [("stdin", (), {}, 0), ("stdout", (), {}, 0), ("stderr", (), {}, 0),
               ("all", ("--json",), {}, 2), ("all", ("--help", "--json"), {}, 0),
               ("all", (), {"CI": "1"}, 0), ("all", ("--no-interactive",), {}, 0)]
    for redirected, args, overrides, expected in samples:
        master, slave = pty.openpty()
        streams = dict(stdin=slave, stdout=slave, stderr=slave)
        if redirected != "all":
            streams[redirected] = subprocess.DEVNULL
        environment = dict(os.environ, TERM="xterm-256color")
        environment.pop("CI", None)
        environment.update(overrides)
        try:
            process = subprocess.Popen([str(binary), *args], env=environment, **streams)
            deadline = time.monotonic() + 4
            output = bytearray()
            while process.poll() is None:
                if time.monotonic() > deadline:
                    raise RuntimeError(f"Entry contract waited for input: {redirected}, {args}.")
                if select.select([master], [], [], 0.02)[0]:
                    output.extend(os.read(master, 65536))
            if process.returncode != expected:
                raise RuntimeError(f"Entry contract failed: {redirected}, {args}.")
            if select.select([master], [], [], 0.1)[0]:
                output.extend(os.read(master, 65536))
                if b"\x1b[?1049h" in output:
                    raise RuntimeError("A disabled entry started the menu renderer.")
        finally:
            if "process" in locals() and process.poll() is None:
                process.kill()
                process.wait()
            os.close(master)
            os.close(slave)


def output_contract(binary, directory, config, fixture):
    """验证真实窄 PTY 的字素截断、NO_COLOR 覆盖和完整 JSON 出口。

    列表与 JSON 使用同一远端长标题，强制颜色只能作用于业务文本。
    """
    title = "中文👨‍👩‍👧‍👦" * 40
    with fixture.lock:
        fixture.issues[1]["title"] = title
    for args, colored, machine in (
        (("issue", "list", "--color", "always"), True, False),
        (("issue", "list", "--color", "auto"), False, False),
        (("issue", "list", "--color", "always", "--json"), False, True),
    ):
        terminal = Terminal(binary, directory, config, args=args, columns=32)
        try:
            terminal.wait_exit(6)
            terminal.read()
            raw = bytes(terminal.all_output)
            if (b"\x1b[" in raw) != colored:
                raise RuntimeError("Color policy or JSON style isolation failed.")
            if machine:
                envelope = json.loads(raw)
                if envelope["items"][0]["data"]["items"][0]["title"] != title:
                    raise RuntimeError("JSON lost the complete Unicode title.")
            elif "…".encode() not in raw or title.encode() in raw:
                raise RuntimeError("The narrow table did not visibly truncate.")
            terminal.close()
        finally:
            terminal.abort()


def display_navigation_contract(binary, directory, config):
    """通过真实 PTY 验证语义色与整组分页，左右键不改变组。

    长组可读到底且 resize 后能回顶部，使用合成 JSON 保持业务零写入。
    """
    for color in ("always", "auto"):
        terminal = Terminal(binary, directory, config, color=color)
        try:
            terminal.choose("Home ·", "repo")
            terminal.wait("Run with options")
            # Bubble Tea仅重画变化的行；固定页脚可能沿用Home已绘制的样式。
            raw = bytes(terminal.all_output)
            expected_colors = {
                b"135": b"Run with options",
                b"39": b"Enter Open",
                b"248": "↑↓ Move".encode(),
                b"208": b"Esc Back",
            }
            if color == "always":
                for code, label in expected_colors.items():
                    pattern = rb"\x1b\[[0-9;]*38;5;" + code + rb"m[^\x1b]*" + re.escape(label)
                    if re.search(pattern, raw) is None:
                        raise RuntimeError(f"The semantic style for {label!r} was not rendered.")
            elif re.search(rb"\x1b\[[0-9;]*m", raw):
                raise RuntimeError("NO_COLOR did not remove semantic styling.")
            terminal.choose("sotea repo", "repo view")
            terminal.group("Overview")
            terminal.wait("A shared project")
            terminal.wait("› Overview")
            terminal.send(b"\x1b[C\t")
            terminal.wait("› Activity")
            terminal.wait("Stars count")
            terminal.send(b"\x1b[D\t")
            terminal.wait("› Links")
            terminal.wait("HTTPS clone URL")
            terminal.send(b"\x1b[Z")
            terminal.wait("› Activity")
            terminal.send(b"\x1b[5~")
            terminal.wait("› Overview")
            terminal.send(b"\x1b[6~")
            terminal.wait("› Activity")
            terminal.home()
            terminal.choose("Home ·", "api")
            terminal.fields_done("api")
            terminal.wait("API path (relative")
            terminal.send(b"/long-display-sample\r")
            terminal.group("Body")
            terminal.wait("Body line 1")
            terminal.send(b"\x1b[F")
            terminal.wait("Body line 60")
            terminal.send(b"\t")
            terminal.wait("Complete tail group")
            terminal.wait("› Tail")
            fcntl.ioctl(terminal.slave, termios.TIOCSWINSZ, struct.pack("HHHH", 16, 68, 0, 0))
            terminal.process.send_signal(signal.SIGWINCH)
            terminal.send(b"\x1b[Z")
            terminal.wait("Body line 1")
            terminal.wait("› Body")
            terminal.home()
            terminal.choose("Home ·", "Quit")
            terminal.close()
        finally:
            terminal.abort()


def nonrepository_context(binary, config):
    """非仓库启动先显示不可用，再用上下文明确补齐目标。

    切换实例会清除旧仓库且不落盘；回到已保留焦点的Home时检查固定状态，不依赖屏外选项。
    """
    before = config.read_bytes()
    with tempfile.TemporaryDirectory(prefix="sotea-p5-nonrepo-") as temporary:
        terminal = Terminal(binary, Path(temporary), config, repository=None, instance=None)
        try:
            terminal.wait("Repository unavailable")
            for field, value in (("Instance ·", "isolated"), ("Repository ·", "team/repo")):
                terminal.choose("Home ·", "Context")
                terminal.choose("Session context", field)
                terminal.wait("New ")
                terminal.send((value + "\r").encode())
                terminal.wait("Home ·")
            terminal.action("issue", "view")
            terminal.choose("Select issues", "p5 issue")
            terminal.wait("issue view · exit 0")
            terminal.action("issue", "view")
            terminal.choose("Select issues", "p5 issue")
            terminal.wait("issue view · exit 0")
            terminal.home()
            terminal.choose("Home ·", "Context")
            terminal.choose("Session context", "Instance ·")
            terminal.wait("New instance")
            terminal.send(b"isolated\r")
            terminal.wait("No repository selected")
            terminal.choose("Home ·", "Quit")
            terminal.close()
            if config.read_bytes() != before:
                raise RuntimeError("A session switch implicitly saved defaults.")
        finally:
            terminal.abort()


def shared_loading_cancel(binary, directory, config, fixture):
    """在持续会话内取消在途候选，Esc 必须回业务菜单并保留终端。

    窗口变化与取消同时发生，不允许写入或在后台继续使用离开的页面。
    """
    terminal = Terminal(binary, directory, config)
    try:
        terminal.wait("Home ·")
        fixture.delay = threading.Event()
        fixture.started.clear()
        terminal.action("issue", "close")
        terminal.wait("Loading...")
        if not fixture.started.wait(3):
            raise RuntimeError("The shared-screen candidate query did not start.")
        fcntl.ioctl(terminal.slave, termios.TIOCSWINSZ, struct.pack("HHHH", 12, 44, 0, 0))
        terminal.process.send_signal(signal.SIGWINCH)
        terminal.send(b"\x1b")
        terminal.wait("issue close · exit 130")
        terminal.send(b"\x1b")
        terminal.wait("sotea issue")
        if fixture.writes or terminal.process.poll() is not None:
            raise RuntimeError("Shared-screen cancellation wrote or terminated the session.")
        fixture.delay.set()
        fixture.delay = None
        terminal.home()
        terminal.choose("Home ·", "Quit")
        terminal.close()
    finally:
        if fixture.delay is not None:
            fixture.delay.set()
            fixture.delay = None
        terminal.abort()


def loading_cancel(binary, directory, config, fixture):
    """验证加载中取消与窗口变化，HTTP provider 必须退出后交还终端。

    远端延迟只在测试线程等待，客户端取消不允许继续写入或留下终端模式改变。
    """
    fixture.delay = threading.Event()
    fixture.started.clear()
    terminal = Terminal(binary, directory, config, args=("issue", "close"))
    try:
        terminal.wait("Loading...")
        if not fixture.started.wait(3):
            raise RuntimeError("The delayed candidate request did not start.")
        fcntl.ioctl(terminal.slave, termios.TIOCSWINSZ, struct.pack("HHHH", 12, 44, 0, 0))
        terminal.process.send_signal(signal.SIGWINCH)
        terminal.send(b"\x03")
        terminal.close(130)
        if fixture.writes:
            raise RuntimeError("Loading cancellation performed a write.")
    finally:
        fixture.delay.set()
        fixture.delay = None
        terminal.abort()


def main():
    """执行两种终端能力下的同一业务会话并归档机器证据。

    结果不能外推为 Terminal／iTerm2 GUI 或 Windows PowerShell 验收。
    """
    parser = argparse.ArgumentParser()
    parser.add_argument("--binary", type=Path, default=ROOT / "dist/p5/darwin-arm64/sotea")
    binary = parser.parse_args().binary.resolve()
    entry_contract(binary)
    for term in ("xterm-256color", "dumb"):
        fixture = Fixture()
        server, thread = serve(fixture)
        try:
            with tempfile.TemporaryDirectory(prefix="sotea-p5-") as temporary:
                directory = Path(temporary)
                subprocess.run(["git", "init", "--quiet", str(directory)], check=True)
                config = directory / "config.json"
                url = f"http://127.0.0.1:{server.server_port}"
                config.write_text(json.dumps({"version": 1, "revision": 1, "instances": {"isolated": {
                    "url": url, "proxy": "direct", "active_account": "alice", "accounts": {
                        "alice": {"source": "environment", "instance_url": url}}}}, "repositories": []}))
                if term != "dumb":
                    loading_cancel(binary, directory, config, fixture)
                    shared_loading_cancel(binary, directory, config, fixture)
                    nonrepository_context(binary, config)
                    terminal = Terminal(binary, directory, config, args=("--yes",))
                    try:
                        terminal.wait("Home ·")
                        terminal.send(b"\x1b")
                        terminal.close()
                    finally:
                        terminal.abort()
                    output_contract(binary, directory, config, fixture)
                    display_navigation_contract(binary, directory, config)
                    fixture.issues[1]["title"] = "p5 issue 1 中文👩🏽‍💻"
                session(binary, directory, config, fixture, term)
                print(f"P5 {term} persistent session passed.", flush=True)
        finally:
            server.shutdown()
            server.server_close()
            thread.join(timeout=2)
    record = {"schema_version": 1, "ui_revision": "quiet-results-breadcrumb-field-hierarchy", "time_utc": datetime.now(timezone.utc).isoformat(), "status": "passed",
              "binary_sha256": hashlib.sha256(binary.read_bytes()).hexdigest(),
              "checks": ["three_stream_entry_matrix", "loading_cancel_130", "resize_during_loading", "shared_loading_cancel_returns_to_parent",
                         "unknown_result_retained_no_replay", "nonrepository_prompt_retention", "session_context_not_saved", "instance_switch_clears_repository",
                         "yes_does_not_select", "narrow_grapheme_table", "no_color_explicit_override",
                         "semantic_options_sections_key_hints", "pgup_pgdn_tab_group_navigation", "arrow_keys_do_not_switch_groups", "long_group_scroll_and_resize",
                         "json_complete_unstyled", "single_alternate_screen", "enter_navigation", "esc_parent_with_focus", "scrollable_results", "menu_api_json_readable_without_leaving_screen", "home_verified_api_profile", "repository_readable_sections", "vt_persistent_session", "plain_persistent_session", "default_no_zero_write", "no_change_no_write",
                         "explicit_clear_preserves_title", "issue_multiselect", "pr_review", "release_draft",
                         "git_argument_array_handoff", "literal_space_argument", "git_nonzero_returns_to_menu", "explicit_default_save", "concurrent_default_save_rejected", "external_configuration_notice", "typed_last_result", "terminal_restored"],
              "scope": "macOS POSIX PTY with synthetic API; GUI hosts and Windows pending"}
    path = ROOT / "tests/integration/.state/p5-terminal-result.json"
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(record, indent=2) + "\n")
    print("P5 VT/plain menu, forms, confirmation, Git handoff, and terminal restore passed.")


if __name__ == "__main__":
    main()
