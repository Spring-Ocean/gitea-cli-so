#!/usr/bin/env python3
"""验证 P2 的 POSIX PTY 确认取消、隐藏输入及终端恢复。

仅使用临时配置和随机本机 HTTP 服务；取消发生在任何密钥持久化之前。
"""

import argparse
from datetime import datetime, timezone
import hashlib
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import os
from pathlib import Path
import pty
import select
import signal
import subprocess
import tempfile
import termios
import threading
import time

ROOT = Path(__file__).resolve().parents[1]


class Handler(BaseHTTPRequestHandler):
    """返回最小匿名版本证据，不提供真实账号或凭证存储。

    所有 PTY 场景在认证请求之前取消，任何 /user 请求都属于测试失败。
    """

    def do_GET(self):
        """只处理版本入口并记录异常身份请求。

        不回显任何请求头，避免将潜在认证材料复制到诊断。
        """
        self.send_response(200 if self.path == "/api/v1/version" else 500)
        self.end_headers()
        self.wfile.write(json.dumps({"version": "28.3.0"}).encode())

    def log_message(self, format_string, *args):
        """抑制 HTTP 默认访问日志。

        结果仅由外层显式记录，不包含完整请求。
        """
        return


def scenario(binary, root_url, directory, hidden):
    """使用实际三流 PTY 验证提示等待中的 Ctrl-C 或 SIGINT。

    终端模式在程序退出后必须恢复，合成 token 不应出现在输出中。
    """
    master, slave = pty.openpty()
    before = termios.tcgetattr(slave)
    environment = dict(os.environ)
    for name in ("SOTEA_TOKEN", "SOTEA_TOKEN_INSTANCE", "SOTEA_INSTANCE", "SOTEA_ACCOUNT", "CI"):
        environment.pop(name, None)
    arguments = [str(binary), "--config", str(directory / "config.json"),
                 "--workdir", str(directory), "--instance", root_url,
                 "auth", "login", "--credential-source", "token"]
    if hidden:
        arguments.append("--confirm-instance")
    process = subprocess.Popen(arguments, env=environment, stdin=slave, stdout=slave,
                               stderr=slave, start_new_session=True)
    output = bytearray()
    prompt = b"API token (input hidden):" if hidden else b"Trust this root URL"
    deadline = time.monotonic() + 10
    try:
        while prompt not in output:
            if time.monotonic() >= deadline or process.poll() is not None:
                raise RuntimeError("Interactive prompt did not become ready.")
            if select.select([master], [], [], 0.1)[0]:
                output.extend(os.read(master, 4096))
        if hidden:
            os.write(master, b"synthetic-hidden-value")
            os.write(master, bytes([3]))
        else:
            process.send_signal(signal.SIGINT)
        if process.wait(timeout=3) != 130:
            raise RuntimeError("Interactive cancellation did not return exit code 130.")
        while select.select([master], [], [], 0.05)[0]:
            output.extend(os.read(master, 4096))
        assert b"synthetic-hidden-value" not in output
        after = termios.tcgetattr(slave)
        # Darwin 在模式切换后设置内核瞬态 PENDIN；它不是回显、规范输入等用户模式。
        transient = getattr(termios, "PENDIN", 0)
        before[3] &= ~transient
        after[3] &= ~transient
        if after != before:
            raise RuntimeError("Terminal input modes were not restored after cancellation.")
        assert not (directory / "config.json").exists()
        return "hidden_token_ctrl_c" if hidden else "confirmation_sigint"
    finally:
        if process.poll() is None:
            process.kill()
            process.wait(timeout=3)
        os.close(master)
        os.close(slave)


def main():
    """启动可控 HTTP 与两类 PTY 场景，保存不含输入内容的证据。

    此测试不代表实际 Terminal／iTerm2 视觉或 Windows 控制台验收。
    """
    parser = argparse.ArgumentParser(description="Verify P2 POSIX PTY cancellation and terminal restore.")
    parser.add_argument("--binary", type=Path, default=ROOT / "dist/p2/darwin-arm64/sotea")
    args = parser.parse_args()
    binary = args.binary.resolve()
    server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    try:
        root_url = f"http://127.0.0.1:{server.server_port}"
        with tempfile.TemporaryDirectory(prefix="sotea-p2-pty-") as directory:
            checks = [scenario(binary, root_url, Path(directory), hidden) for hidden in (False, True)]
        result = {"schema_version": 1, "time_utc": datetime.now(timezone.utc).isoformat(), "status": "passed",
                  "binary_sha256": hashlib.sha256(binary.read_bytes()).hexdigest(), "checks": checks}
        (ROOT / "tests/integration/.state/p2-terminal-result.json").write_text(json.dumps(result, indent=2) + "\n")
        print("P2 PTY cancellation, hidden token input, and terminal restoration passed.")
    finally:
        server.shutdown()
        server.server_close()
        thread.join(timeout=2)


if __name__ == "__main__":
    main()
