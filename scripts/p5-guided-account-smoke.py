#!/usr/bin/env python3
"""以合成Git helper和本机API验证账号向导、取消与浏览续接。

可选截图仅渲染合成PTY内容，不控制GUI，不读取真实凭证；截图不代表iTerm2验收。
"""
import argparse
import base64
from datetime import datetime, timezone
import hashlib
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import importlib.util
import json
from pathlib import Path
import subprocess
import tempfile
import threading
import urllib.parse

ROOT = Path(__file__).resolve().parents[1]
SPEC = importlib.util.spec_from_file_location("p5_terminal", ROOT / "scripts/p5-terminal-smoke.py")
DRIVER = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(DRIVER)
SECRET = "synthetic-wizard-credential"


def serve():
    """提供用于临时Git身份绑定的API，不允许HTTP写操作。

    未知路径严格404，身份请求只接受隔离helper的Basic凭证。
    """
    basic = "Basic " + base64.b64encode(("alice:" + SECRET).encode()).decode()
    repository = {"id": 1, "name": "repo", "full_name": "team/repo", "default_branch": "main",
                  "description": "Shared project", "owner": {"login": "team"}}
    release = {"id": 10, "tag_name": "0.6.3", "name": "v0.6.3", "draft": False,
               "body": "# v0.6.3\n\n### New features\n\n- **Account browser** for repositories and organizations\n- [x] Guided account connection\n\n### Improvements\n\n> Readable navigation, input fields and recovery actions.\n\n~~~go\nfmt.Println(\"hello, sotea\")\n~~~\n\n[Release documentation](https://example.invalid/releases)"}
    state = {"writes": 0, "identity_reads": 0}

    class Handler(BaseHTTPRequestHandler):
        """只回复合成数据，不输出认证头或访问日志。

        服务线程的生命周期由测试main统一关闭。
        """
        def do_GET(self):
            """固定版本探测允许匿名，其余端点验证合成身份。

            这与未知来源先匿名探测、随后显式凭证验证的产品边界一致。
            """
            path = urllib.parse.urlsplit(self.path).path
            if path == "/api/v1/version":
                value, status = {"version": "28.3.0"}, 200
            elif self.headers.get("Authorization") != basic:
                value, status = {"message": "invalid fixture credential"}, 401
            elif path == "/api/v1/user":
                state["identity_reads"] += 1
                value, status = {"login": "alice", "full_name": "Alice Ocean"}, 200
            elif path == "/api/v1/repos/team/repo":
                value, status = repository, 200
            elif path == "/api/v1/user/repos":
                value, status = [repository], 200
            elif path == "/api/v1/user/orgs":
                value, status = [], 200
            elif path == "/api/v1/repos/team/repo/releases":
                value, status = [release, {"id": 11, "tag_name": "0.7.0", "name": "Upcoming release", "draft": True}], 200
            elif path == "/api/v1/repos/team/repo/releases/tags/0.6.3":
                value, status = release, 200
            else:
                value, status = {"message": "unknown fixture path"}, 404
            self.send_response(status)
            self.end_headers()
            self.wfile.write(json.dumps(value).encode())

        def do_POST(self):
            """记录禁止发生的业务写入。

            绑定只应修改临时本地配置，不能创建或修改远端资源。
            """
            state["writes"] += 1
            self.send_response(405)
            self.end_headers()

        do_PATCH = do_POST
        do_PUT = do_POST
        do_DELETE = do_POST

        def log_message(self, *args):
            """关闭HTTP访问日志。

            验证记录只包含结果和产物摘要。
            """
            return

    server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    worker = threading.Thread(target=server.serve_forever, daemon=True)
    worker.start()
    return server, worker, state


def capture(terminal, directory, name):
    """把合成PTY的ANSI屏幕渲染为可目视检查的PNG。

    pyte和Pillow仅为可选验证依赖，不进入Go产品或普通回归运行。
    """
    if directory is None:
        return
    import pyte
    from PIL import Image, ImageDraw, ImageFont
    terminal.read(0.2)
    screen = pyte.Screen(120, 32)
    pyte.Stream(screen).feed(terminal.all_output.decode(errors="replace"))
    font = ImageFont.truetype("/System/Library/Fonts/Menlo.ttc", 20)
    bold = ImageFont.truetype("/System/Library/Fonts/Menlo.ttc", 20, index=1)
    symbols = ImageFont.truetype("/System/Library/Fonts/Supplemental/Arial Unicode.ttf", 20)
    cell, height = 12, 28
    image = Image.new("RGB", (120 * cell, 32 * height), "#13171c")
    draw = ImageDraw.Draw(image)
    colors = {"default": "#d9dce3", "black": "#13171c", "red": "#ff5f5f",
              "green": "#5fff87", "brown": "#ffaf00", "blue": "#5fafff",
              "magenta": "#af5fff", "cyan": "#00d7af", "white": "#eeeeee"}
    for row in range(32):
        for column in range(120):
            char = screen.buffer[row][column]
            color = colors.get(char.fg, "#" + char.fg)
            if char.bg != "default":
                background = colors.get(char.bg, "#" + char.bg)
                draw.rectangle((column * cell, row * height, (column + 1) * cell, (row + 1) * height), fill=background)
            face = bold if char.bold else font
            if len(char.data) == 1 and "\u2500" <= char.data <= "\u257f":
                face = symbols
            try:
                draw.text((column * cell, row * height), char.data, font=face, fill=color)
            except ValueError:
                draw.text((column * cell, row * height), char.data, font=font, fill="#d9dce3")
    directory.mkdir(parents=True, exist_ok=True)
    image.save(directory / (name + ".png"))
    (directory / (name + ".txt")).write_text("\n".join(screen.display))


def connect_steps(terminal, secret_cancel=False):
    """使用真实按键依次填写实例、账号和凭证来源。

    Secret取消场景在提交前退出，不能通过系统密钥库存入测试token。
    """
    terminal.wait("Step 1 of 4")
    terminal.send(b"\r")
    terminal.wait("Step 2 of 4")
    terminal.send(b"\r")
    terminal.choose("Step 3 of 4", "token" if secret_cancel else "git")
    if secret_cancel:
        terminal.wait("Access token")
        terminal.send(b"synthetic-never-save-this")
        if b"synthetic-never-save-this" in terminal.all_output:
            raise RuntimeError("The input box exposed its secret value.")
        terminal.send(b"\x1b")
        terminal.wait("Step 3 of 4")
        terminal.send(b"\x1b")
        terminal.wait("Step 2 of 4")
        terminal.send(b"\x1b")
        terminal.wait("Step 1 of 4")
        terminal.send(b"\x1b")
        terminal.wait("Account")
        return
    terminal.wait("Step 4 of 4")


def main():
    """完成零写取消与临时Git身份的显式绑定，记录同一产物的实际结果。

    只创建临时repo／helper／config；外部真实系统和GUI验收另行记录。
    """
    parser = argparse.ArgumentParser()
    parser.add_argument("--binary", type=Path, default=ROOT / "dist/p5/darwin-arm64/sotea")
    parser.add_argument("--screenshots", type=Path)
    args = parser.parse_args()
    binary = args.binary.resolve()
    server, worker, state = serve()
    terminal = None
    try:
        with tempfile.TemporaryDirectory(prefix="sotea-guided-account-") as temporary:
            directory = Path(temporary)
            url = f"http://127.0.0.1:{server.server_port}"
            subprocess.run(["git", "init", "--quiet", str(directory)], check=True)
            helper = directory / "credential-helper"
            helper.write_text("#!/bin/sh\n[ \"$1\" = get ] || exit 0\ncat >/dev/null\nprintf '%s\\n' 'username=alice' 'password=" + SECRET + "' ''\n")
            helper.chmod(0o700)
            subprocess.run(["git", "-C", str(directory), "config", "credential.helper", str(helper)], check=True)
            subprocess.run(["git", "-C", str(directory), "remote", "add", "origin", url + "/team/repo.git"], check=True)
            config = directory / "config.json"
            config.write_text(json.dumps({"version": 1, "revision": 1, "instances": {"isolated": {
                "url": url, "proxy": "direct", "accounts": {}}}, "repositories": []}))
            original = config.read_bytes()
            terminal = DRIVER.Terminal(binary, directory, config, columns=120, color="always",
                                       environment_extra={"SOTEA_TOKEN": "", "SOTEA_TOKEN_INSTANCE": ""})
            terminal.choose("Home ·", "auth")
            terminal.choose("Account", "auth login")
            capture(terminal, args.screenshots, "account-input")
            connect_steps(terminal, secret_cancel=True)
            if config.read_bytes() != original:
                raise RuntimeError("Cancelling the account wizard saved configuration.")
            terminal.choose("Account", "auth login")
            terminal.wait("Step 1 of 4")
            terminal.send(b"\r")
            terminal.wait("Step 2 of 4")
            terminal.send(b"\r")
            terminal.wait("Step 3 of 4")
            changed = json.loads(config.read_text())
            changed["revision"] += 1
            config.write_text(json.dumps(changed))
            original = config.read_bytes()
            before = state["identity_reads"]
            terminal.choose("Step 3 of 4", "git")
            terminal.wait("CONFIG_CONFLICT")
            if state["identity_reads"] != before:
                raise RuntimeError("A stale account wizard sent credentials before checking configuration.")
            terminal.choose("The selected context changed", "retry")
            terminal.wait("Step 1 of 4")
            terminal.send(b"\x1b")
            terminal.wait("Account")
            terminal.home()
            terminal.choose("Home ·", "Browse account")
            terminal.wait("Connect an account to browse")
            capture(terminal, args.screenshots, "account-recovery")
            terminal.choose("Connect an account to browse", "connect")
            connect_steps(terminal)
            capture(terminal, args.screenshots, "account-review")
            if config.read_bytes() != original:
                raise RuntimeError("Previewing an identity saved it before confirmation.")
            terminal.choose("Step 4 of 4", "save")
            terminal.wait("Account connected")
            terminal.choose("Account connected", "continue")
            terminal.wait("Browse account ·")
            saved = json.loads(config.read_text())
            if saved["instances"]["isolated"]["accounts"]["alice"]["source"] != "git":
                raise RuntimeError("The verified helper connection was not saved.")
            if SECRET in config.read_text():
                raise RuntimeError("A Git secret reached ordinary configuration.")
            terminal.choose("Browse account ·", "Back to Home")
            terminal.choose("Home ·", "release")
            capture(terminal, args.screenshots, "release-navigation")
            terminal.choose("sotea release", "release view")
            terminal.wait("Select release")
            terminal.wait("◇ Draft")
            if b"draft=false" in terminal.output or b"draft=true" in terminal.output:
                raise RuntimeError("The release selector exposed raw draft booleans.")
            capture(terminal, args.screenshots, "release-selector")
            terminal.choose("Select release", "0.6.3")
            terminal.group("Body")
            terminal.wait("New features")
            capture(terminal, args.screenshots, "release-markdown")
            terminal.home()
            terminal.choose("Home ·", "Quit")
            terminal.close()
            if SECRET.encode() in terminal.all_output or state["writes"]:
                raise RuntimeError("The wizard exposed credentials or wrote to the API.")
            terminal = None
        record = {"schema_version": 1, "status": "passed", "time_utc": datetime.now(timezone.utc).isoformat(),
                  "binary_sha256": hashlib.sha256(binary.read_bytes()).hexdigest(),
                  "checks": ["guided_account_cancel_no_save", "hidden_token_no_echo", "temporary_git_identity_connect",
                             "preview_before_explicit_save", "browse_resumes_after_connection", "no_secret_in_config",
                             "stale_wizard_blocks_identity_request", "stale_wizard_can_restart",
                             "conditional_draft_badge", "release_markdown_in_session", "zero_http_writes", "terminal_restored"],
                  "scope": "Synthetic local API and POSIX PTY; GUI and Windows remain pending"}
        (ROOT / "tests/integration/.state/p5-guided-account-result.json").write_text(json.dumps(record, indent=2) + "\n")
        print("P5 guided account, draft badges, Markdown and scoped cancellation passed.")
    finally:
        if terminal is not None:
            terminal.abort()
        server.shutdown()
        server.server_close()
        worker.join(timeout=2)


if __name__ == "__main__":
    main()
