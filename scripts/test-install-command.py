#!/usr/bin/env python3
"""通过真实 loopback HTTP 验证公开 Windows 安装命令的字节解码。

不访问外网、不安装程序、不改 PATH；Windows 完整安装另由发布回归执行。
"""

import argparse
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
import subprocess
import threading

ROOT = Path(__file__).resolve().parents[1]


def run_http_command_tests(powershell, installer_path):
    """用不同 Content-Type 和 BOM 向真实 PowerShell 提供相同 UTF-8 源码。

    只监听回环且只提供固定测试路径；无论子进程结果如何都关闭并等待服务线程。
    """
    payload = installer_path.read_bytes()
    bom = bytes([239, 187, 191])
    if not payload.startswith(bom):
        raise AssertionError("Keep the installer UTF-8 BOM for PowerShell 5.1 file execution.")
    content_types = {
        "utf8": "text/plain; charset=utf-8",
        "latin1": "text/plain; charset=iso-8859-1",
        "plain": "text/plain",
        "binary": "application/octet-stream",
    }
    bodies = {}
    for label, content_type in content_types.items():
        bodies["/" + label + "-bom"] = (content_type, payload)
        bodies["/" + label + "-no-bom"] = (content_type, payload[len(bom):])

    class Handler(BaseHTTPRequestHandler):
        """只读取固定字节夹具，不执行脚本或暴露本地目录。

        拒绝未定义路径，下载失败场景使用明确 HTTP 503。
        """

        def do_GET(self):
            """按请求路径提供固定 Content-Type 与原始字节。

            Content-Length 固定后关闭响应，不使用编码转换或隐藏网络替身。
            """
            if self.path not in bodies:
                self.send_error(503 if self.path == "/error" else 404)
                return
            content_type, body = bodies[self.path]
            self.send_response(200)
            self.send_header("Content-Type", content_type)
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)

        def log_message(self, format, *args):
            """抑制临时 HTTP 请求日志，保留 PowerShell 输出作为回归结果。

            不记录源码、路径参数或任何用户数据。
            """
            return

    server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    worker = threading.Thread(target=server.serve_forever, daemon=True)
    worker.start()
    try:
        subprocess.run([
            powershell, "-NoProfile", "-NonInteractive", "-File",
            str(ROOT / "scripts/test-install-command.ps1"),
            "-FixtureBaseURL", "http://127.0.0.1:" + str(server.server_port),
        ], cwd=ROOT, check=True, timeout=180)
    finally:
        server.shutdown()
        server.server_close()
        worker.join(timeout=5)
        if worker.is_alive():
            raise RuntimeError("The HTTP bootstrap fixture did not stop.")


def main():
    """选择真实 PowerShell 并校验本地或最终发布脚本。

    文件路径仅来自调用者显式参数；不解析用户配置或下载远端程序。
    """
    parser = argparse.ArgumentParser(description="Test documented PowerShell commands against loopback HTTP bytes.")
    parser.add_argument("--powershell", required=True)
    parser.add_argument("--installer-path", type=Path, default=ROOT / "scripts/install.ps1")
    args = parser.parse_args()
    run_http_command_tests(args.powershell, args.installer_path)


if __name__ == "__main__":
    main()
