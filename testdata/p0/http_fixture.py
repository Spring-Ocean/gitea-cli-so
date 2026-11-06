"""提供本机可控的 HTTP／TLS 夹具。

支持状态、延迟、断连和请求计数；不存储请求体或认证头。
"""

from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import socket
import ssl
import threading
import time
from urllib.parse import parse_qs, urlsplit


class FixtureHandler(BaseHTTPRequestHandler):
    """处理固定的故障模拟路由。

    诊断仅保存在有界的事件列表，不写默认访问日志。
    """

    def log_message(self, format, *args):
        """禁用默认访问日志以保护测试请求上下文。

        测试通过 events 读取有限的非敏感请求摘要。
        """
        return

    def do_GET(self):
        """按固定路由返回状态、延迟响应或主动断连。

        事件保留方法、路径和请求体长度，数组有界。
        """
        target = urlsplit(self.path)
        size = int(self.headers.get("Content-Length", "0"))
        if not 0 <= size <= 1024 * 1024:
            self.send_error(413)
            return
        if size:
            self.rfile.read(size)
        with self.server.event_lock:
            if target.path != "/events":
                self.server.events.append({"method": self.command, "path": target.path, "body_size": size})
                self.server.events = self.server.events[-100:]
            events = list(self.server.events)
        if target.path == "/disconnect":
            self.connection.shutdown(socket.SHUT_RDWR)
            self.connection.close()
            return
        if target.path == "/delay":
            seconds = float(parse_qs(target.query).get("seconds", ["0.3"])[0])
            time.sleep(max(0, min(seconds, 2)))
        status = int(target.path.rsplit("/", 1)[-1]) if target.path.startswith("/status/") else 200
        body = json.dumps(events if target.path == "/events" else {"ok": True}).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        try:
            self.wfile.write(body)
        except (BrokenPipeError, ConnectionResetError, ssl.SSLError):
            return

    def do_POST(self):
        """将写请求用于相同的故障路由。

        请求计数使后续测试能够识别客户端是否意外重放写入。
        """
        self.do_GET()


class FixtureServer:
    """管理只绑定 loopback 的短生命周期服务。

    可选证书仅来自测试目录，退出时关闭监听和后台线程。
    """

    def __init__(self, cert=None, key=None):
        """分配随机本机端口并按需启用 TLS。

        证书与私钥必须同时给出，实际启动由上下文入口完成。
        """
        self.server = ThreadingHTTPServer(("127.0.0.1", 0), FixtureHandler)
        self.server.events = []
        self.server.event_lock = threading.Lock()
        if cert:
            context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
            context.load_cert_chain(cert, key)
            self.server.socket = context.wrap_socket(self.server.socket, server_side=True)
        self.url = f"{'https' if cert else 'http'}://127.0.0.1:{self.server.server_port}"
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)

    def __enter__(self):
        """启动服务并返回具有 URL 的夹具对象。

        调用者使用 context manager 保证停止监听。
        """
        self.thread.start()
        return self

    def __exit__(self, kind, value, traceback):
        """停止服务并等待监听线程结束。

        测试异常仍交由上层处理，不被清理过程吞掉。
        """
        self.server.shutdown()
        self.server.server_close()
        self.thread.join(timeout=3)
