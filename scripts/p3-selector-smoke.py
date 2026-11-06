#!/usr/bin/env python3
"""通过真实 POSIX PTY 验证跨页／跨搜索勾选与写入确认。

只访问随机本机 HTTP 服务和临时配置，不操作真实 Gitea 数据或系统凭证。
"""

import argparse
import fcntl
from datetime import datetime, timezone
import hashlib
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import os
from pathlib import Path
import pty
import re
import select
import subprocess
import struct
import tempfile
import termios
import threading
import time
import urllib.parse

ROOT = Path(__file__).resolve().parents[1]


class State:
    """保存本地合成 Issue 与实际写入编号。

    读写统一通过锁，不用客户端 EOF 作为共享内存同步。
    """

    def __init__(self):
        """建立超过一页的候选和带控制字符的远端标题。

        搜索匹配位于第二页，证明选择不是只搜索已加载第一页。
        """
        self.lock = threading.Lock()
        self.writes = []
        self.issues = {index: {"id": index, "number": index, "title": f"item {index}",
                               "body": "", "state": "open", "updated_at": "initial",
                               "assignees": [], "labels": [], "milestone": None}
                       for index in range(1, 42)}
        self.issues[1]["title"] = "unsafe \x1b]52;c;ATTACK\x07 title"
        self.issues[31]["title"] = "needle first"
        self.issues[32]["title"] = "needle second"


def scenario(binary, root_url, configuration, state, cancel):
    """在真实三流 PTY 中执行不带编号的 issue close。

    成功场景勾选跨页目标，确认前写入必须为零；取消场景不得新增写入。
    """
    master, slave = pty.openpty()
    fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 28, 100, 0, 0))
    original = termios.tcgetattr(slave)
    environment = dict(os.environ, SOTEA_TOKEN="synthetic-selector-token", SOTEA_TOKEN_INSTANCE="isolated", TERM="xterm-256color")
    for name in ("SOTEA_INSTANCE", "SOTEA_ACCOUNT", "CI"):
        environment.pop(name, None)
    command = [str(binary), "--config", str(configuration), "--workdir", str(configuration.parent),
               "--instance", "isolated", "--repo", "team/repo", "issue", "close"]
    process = subprocess.Popen(command, env=environment, stdin=slave, stdout=slave, stderr=slave, start_new_session=True)
    output = bytearray()
    with state.lock:
        before = list(state.writes)

    def wait_for(text):
        """有界等待可见模型状态，非协议终端输出只保留在内存。

        不向外输出完整屏幕或凭证，超时使当前测试失败。
        """
        deadline = time.monotonic() + 10
        marker = text.encode()
        while marker not in output:
            if process.poll() is not None or time.monotonic() > deadline:
                plain = re.sub(r"\x1b\[[0-?]*[ -/]*[@-~]", "", output.decode(errors="replace"))
                safe = "".join(character for character in plain if character.isprintable() or character == "\n")
                raise RuntimeError(f"Selector did not reach {text!r}; exit={process.poll()}; screen={safe[-1200:]!r}")
            if select.select([master], [], [], 0.1)[0]:
                output.extend(os.read(master, 16384))

    def send(value):
        """发送明确按键并重置等待缓存，避免误匹配上一轮画面。

        所有键都来自测试场景，不从远端数据生成输入。
        """
        output.clear()
        os.write(master, value)

    try:
        wait_for("item 2" if cancel else "unsafe")
        if cancel:
            send(b" \x03")
            expected = 130
        else:
            send(b"\r")
            wait_for("Select at least one item")
            send(b" ")
            wait_for("Selected 1")
            send(b"n")
            wait_for("needle first")
            send(b" ")
            wait_for("Selected 2")
            send(b"/")
            wait_for("Search candidates")
            send(b"needle\r")
            wait_for("Search: needle")
            time.sleep(0.1)
            send(b"\x1b[B ")
            wait_for("Selected 3")
            send(b"s")
            wait_for("#1 open unsafe")
            send(b"\x1b[B ")
            wait_for("Selected 2")
            send(b"\r")
            wait_for("Apply this exact Issue operation")
            with state.lock:
                assert state.writes == before
            send(b"y\r")
            expected = 0
        if process.wait(timeout=10) != expected:
            raise RuntimeError("Interactive Issue command returned an unexpected exit status.")
        with state.lock:
            delta = state.writes[len(before):]
        assert delta == ([] if cancel else [1, 32])
        after = termios.tcgetattr(slave)
        transient = getattr(termios, "PENDIN", 0)
        original[3] &= ~transient
        after[3] &= ~transient
        assert original == after
        assert b"\x1b]52;c;ATTACK\x07" not in output
    finally:
        if process.poll() is None:
            process.kill()
            process.wait(timeout=3)
        os.close(master)
        os.close(slave)


def main():
    """运行本地候选服务与成功／取消两类真实选择场景。

    记录是 PTY 原型验证，不代替 Windows 或 GUI 终端宿主验收。
    """
    parser = argparse.ArgumentParser(description="Verify the P3 selector in a real POSIX PTY.")
    parser.add_argument("--binary", type=Path, default=ROOT / "dist/p3/darwin-arm64/sotea")
    args = parser.parse_args()
    binary = args.binary.resolve()
    state = State()

    class Handler(BaseHTTPRequestHandler):
        """实现当前选择器和状态提交需要的最小 API。

        各候选状态可变，提交后读回返回真实更新结果。
        """

        def response(self, data):
            """返回完整 JSON 响应，不记录请求认证头。

            所有资源都是当前进程的合成内存数据。
            """
            self.send_response(200)
            self.end_headers()
            self.wfile.write(json.dumps(data).encode())

        def do_GET(self):
            """根据完整查询集分页和搜索，保留稳定编号。

            type=issues 必须明确提供，避免把未实现类型筛选当作通过。
            """
            path = urllib.parse.urlsplit(self.path)
            if path.path == "/api/v1/user":
                self.response({"login": "alice"})
            elif path.path == "/api/v1/version":
                self.response({"version": "28.3.0"})
            else:
                with state.lock:
                    if path.path.endswith("/issues"):
                        query = urllib.parse.parse_qs(path.query)
                        assert query["type"] == ["issues"]
                        values = [value.copy() for value in state.issues.values()
                                  if value["state"] == query.get("state", ["open"])[0]
                                  and query.get("q", [""])[0] in value["title"]]
                        page, limit = int(query.get("page", ["1"])[0]), int(query.get("limit", ["30"])[0])
                        self.response(values[(page-1)*limit:page*limit])
                    else:
                        self.response(state.issues[int(path.path.rsplit("/", 1)[1])])

        def do_PATCH(self):
            """记录每个明确提交编号，并更新供写后核对的状态。

            只允许测试预期的状态 PATCH，不创建其他资源。
            """
            number = int(self.path.rsplit("/", 1)[1])
            payload = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
            with state.lock:
                state.writes.append(number)
                state.issues[number]["state"] = payload["state"]
                state.issues[number]["updated_at"] = str(len(state.writes))
                self.response(state.issues[number])

        def log_message(self, format_string, *arguments):
            """抑制默认访问日志，结果仅输出明确摘要。

            凭证和远端控制字符不复制到共享诊断。
            """
            return

    server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    try:
        root_url = f"http://127.0.0.1:{server.server_port}"
        with tempfile.TemporaryDirectory(prefix="sotea-p3-selector-") as directory:
            config = Path(directory) / "config.json"
            config.write_text(json.dumps({"version": 1, "revision": 1, "instances": {"isolated": {
                "url": root_url, "proxy": "direct", "active_account": "alice",
                "accounts": {"alice": {"source": "environment", "instance_url": root_url}}}}, "repositories": []}))
            for cancel in (False, True):
                scenario(binary, root_url, config, state, cancel)
        result = {"schema_version": 1, "time_utc": datetime.now(timezone.utc).isoformat(), "status": "passed",
                  "binary_sha256": hashlib.sha256(binary.read_bytes()).hexdigest(),
                  "checks": ["no_default_selection", "cross_page_and_search_retention", "selected_review_removal",
                             "no_write_before_confirmation", "exact_stable_ids", "cancel_130_no_write", "terminal_restored"]}
        (ROOT / "tests/integration/.state/p3-selector-result.json").write_text(json.dumps(result, indent=2) + "\n")
        print("P3 selector paging, search, exact confirmation, cancellation, and terminal restore passed.")
    finally:
        server.shutdown()
        server.server_close()
        thread.join(timeout=2)


if __name__ == "__main__":
    main()
