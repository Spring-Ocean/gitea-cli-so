#!/usr/bin/env python3
"""验证 PR 多选与 Release 标签单选进入正确应用服务后才提交。

使用真实 POSIX PTY 和合成 API，不作为 GUI 终端或 Windows 宿主证据。
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
import select
import struct
import subprocess
import tempfile
import termios
import threading
import time
import urllib.parse

ROOT = Path(__file__).resolve().parents[1]


def scenario(binary, config, state, lock, kind, cancel):
    """以三流终端运行缺参写命令，验证无默认目标及确认前零写入。

    PR 明确选择两个编号，Release 只选第二个完整标签；取消必须恢复终端。
    """
    master, slave = pty.openpty()
    fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack('HHHH', 28, 100, 0, 0))
    original = termios.tcgetattr(slave)
    env = dict(os.environ, SOTEA_TOKEN='synthetic-p4-selector-token', SOTEA_TOKEN_INSTANCE='isolated', TERM='xterm-256color')
    for name in ('CI', 'SOTEA_INSTANCE', 'SOTEA_ACCOUNT'):
        env.pop(name, None)
    args = ['pr', 'close'] if kind == 'pr' else ['release', 'edit', '--title', 'updated']
    process = subprocess.Popen([str(binary), '--config', str(config), '--workdir', str(config.parent), '--instance', 'isolated', '--repo', 'team/repo', *args],
                               env=env, stdin=slave, stdout=slave, stderr=slave, start_new_session=True)
    output = bytearray()
    with lock:
        before = list(state['writes'])

    def wait_for(marker):
        """只等待指定可见状态，不依赖完整终端样式快照。

        输出与合成 token 留在进程内，超时仅报告阶段与退出码。
        """
        deadline = time.monotonic() + 10
        while marker.encode() not in output:
            if process.poll() is not None or time.monotonic() > deadline:
                raise RuntimeError(f'P4 selector did not reach {marker!r}; exit={process.poll()}.')
            if select.select([master], [], [], 0.1)[0]:
                output.extend(os.read(master, 16384))

    def send(keys):
        """清空旧屏幕缓存后发送场景明确的按键。

        输入不由远端内容生成，避免把控制内容当作操作。
        """
        output.clear()
        os.write(master, keys)

    try:
        wait_for('candidate second')
        if kind == 'pr':
            send(b'\r')
            wait_for('Select at least one item')
        with lock:
            assert state['writes'] == before
        if cancel:
            send(b'\x03')
        else:
            if kind == 'pr':
                send(b' ')
                wait_for('Selected 1')
                send(b'\x1b[B ')
                wait_for('Selected 2')
            else:
                send(b'\x1b[B')
            send(b'\r')
            wait_for('Apply this exact PR operation' if kind == 'pr' else 'Apply this exact release operation')
            with lock:
                assert state['writes'] == before
            send(b'y\r')
        deadline = time.monotonic() + 10
        while process.poll() is None and time.monotonic() < deadline:
            if select.select([master], [], [], 0.1)[0]:
                output.extend(os.read(master, 16384))
        assert process.poll() == (130 if cancel else 0), (kind, cancel, process.poll())
        with lock:
            actual = state['writes'][len(before):]
        expected = [] if cancel else ([('pr', 1), ('pr', 2)] if kind == 'pr' else [('release', 'p4/中文-second')])
        assert actual == expected, (kind, actual)
        restored = termios.tcgetattr(slave)
        transient = getattr(termios, 'PENDIN', 0)
        original[3] &= ~transient
        restored[3] &= ~transient
        assert original == restored
    finally:
        if process.poll() is None:
            process.kill()
            process.wait(timeout=3)
        os.close(master)
        os.close(slave)


def main():
    """构造最小 PR 与草稿资源，覆盖两类新入口的真实按键路由。

    API 与写入记录共用锁，结束关闭服务，记录不包含凭证或终端原文。
    """
    parser = argparse.ArgumentParser(description='Verify PR and Release selection in a real POSIX PTY.')
    parser.add_argument('--binary', type=Path, default=ROOT / 'dist/p4/darwin-arm64/sotea')
    binary = parser.parse_args().binary.resolve()
    lock = threading.Lock()
    state = {'writes': [], 'pr': {}, 'release': {}}
    for number in (1, 2):
        state['pr'][number] = {'id': number, 'number': number, 'title': 'candidate ' + ('first' if number == 1 else 'second'),
                               'state': 'open', 'head': {'sha': 'a'*40, 'ref': 'feature'}, 'base': {'ref': 'main'}}
        tag = 'p4/中文-' + ('first' if number == 1 else 'second')
        state['release'][tag] = {'id': number, 'tag_name': tag, 'name': 'candidate ' + ('first' if number == 1 else 'second'), 'draft': True, 'prerelease': False}

    class Handler(BaseHTTPRequestHandler):
        """仅提供选择、前置重核和写后读回所需的合成资源。

        路由严格区分 PR 编号、Release 标签和实际 Release ID。
        """
        def respond(self, value, status=200):
            """响应完整 JSON，不记录认证头或访问日志。

            404 用于不存在的实际 Git 标签，草稿仍可读取。
            """
            self.send_response(status)
            self.end_headers()
            self.wfile.write(json.dumps(value).encode())

        def do_GET(self):
            """读取稳定候选，标签路径解码后精确匹配对象。

            每个场景最多两项，不引入额外分页或搜索模拟。
            """
            path = urllib.parse.unquote(urllib.parse.urlsplit(self.path).path)
            with lock:
                if path.endswith('/version'):
                    self.respond({'version': '28.3.0'})
                elif path.endswith('/user'):
                    self.respond({'login': 'alice'})
                elif '/releases/tags/' in path:
                    self.respond(state['release'][path.split('/releases/tags/')[1]])
                elif path.endswith('/releases'):
                    self.respond(list(state['release'].values()))
                elif '/tags/' in path:
                    self.respond({}, 404)
                elif path.endswith('/pulls'):
                    self.respond([value for value in state['pr'].values() if value['state'] == 'open'])
                else:
                    self.respond(state['pr'][int(path.rsplit('/', 1)[1])])

        def do_PATCH(self):
            """记录稳定目标后更新内存对象，供应用服务核对。

            Release 必须按 ID 写入，但用户选择结果必须仍为完整标签。
            """
            path = urllib.parse.urlsplit(self.path).path
            payload = json.loads(self.rfile.read(int(self.headers['Content-Length'])))
            number = int(path.rsplit('/', 1)[1])
            with lock:
                if '/pulls/' in path:
                    value = state['pr'][number]
                    state['writes'].append(('pr', number))
                else:
                    value = next(item for item in state['release'].values() if item['id'] == number)
                    state['writes'].append(('release', value['tag_name']))
                value.update(payload)
                self.respond(value)

        def log_message(self, *args):
            """抑制服务器默认日志，避免认证或远端内容外泄。

            验证结果只记录场景名称和产物摘要。
            """
            return

    server = ThreadingHTTPServer(('127.0.0.1', 0), Handler)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    try:
        with tempfile.TemporaryDirectory(prefix='sotea-p4-selector-') as directory:
            config = Path(directory) / 'config.json'
            url = f'http://127.0.0.1:{server.server_port}'
            config.write_text(json.dumps({'version': 1, 'revision': 1, 'instances': {'isolated': {'url': url, 'proxy': 'direct', 'active_account': 'alice',
                              'accounts': {'alice': {'source': 'environment', 'instance_url': url}}}}, 'repositories': []}))
            for kind in ('pr', 'release'):
                for cancel in (True, False):
                    scenario(binary, config, state, lock, kind, cancel)
        result = {'schema_version': 1, 'time_utc': datetime.now(timezone.utc).isoformat(), 'status': 'passed',
                  'binary_sha256': hashlib.sha256(binary.read_bytes()).hexdigest(),
                  'checks': ['pr_explicit_multiselect', 'release_exact_unicode_slash_tag_single_selection', 'single_enter_accepts_focused_tag', 'no_implicit_target_before_enter', 'no_write_before_confirmation', 'cancel_130_no_write', 'terminal_restored'],
                  'scope': 'POSIX PTY with synthetic API; GUI and Windows hosts remain pending'}
        (ROOT / 'tests/integration/.state/p4-selector-result.json').write_text(json.dumps(result, indent=2) + '\n')
        print('P4 PR and Release selection, exact confirmation, cancellation, and terminal restore passed.')
    finally:
        server.shutdown()
        server.server_close()
        thread.join(timeout=2)


if __name__ == '__main__':
    main()
