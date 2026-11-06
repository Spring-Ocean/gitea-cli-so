#!/usr/bin/env python3
"""演练 P0 的隔离 helper、故障服务和私有 CA。

仅验证测试基础设施，不作为 sotea 业务实现的测试替身。
"""

from datetime import datetime, timezone
import http.client
import json
import os
from pathlib import Path
import shlex
import ssl
import subprocess
import sys
import tempfile
import urllib.error
import urllib.request

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT / "testdata/p0"))
from http_fixture import FixtureServer


def execute(args, env=None, data=None, timeout=10):
    """执行只使用合成输入的基础设施命令。

    捕获所有输出，报告中仅写结论，不写 helper 凭证。
    """
    result = subprocess.run(args, env=env, input=data, text=True, capture_output=True, timeout=timeout)
    if result.returncode:
        raise RuntimeError(f"{Path(args[0]).name} 夹具命令失败")
    return result.stdout


def helper_checks(directory):
    """在独立 Git 配置上下文中核对 helper 的四种行为。

    不覆盖 HOME，不加载系统及用户 Git 配置，不调用真实凭证库。
    """
    env = dict(os.environ, GIT_CONFIG_NOSYSTEM="1", GIT_CONFIG_GLOBAL=os.devnull,
               GIT_TERMINAL_PROMPT="0", SOTEA_TEST_HELPER_LOG=str(directory / "helper.jsonl"))
    for key in list(env):
        if key.startswith("GIT_CONFIG_KEY_") or key.startswith("GIT_CONFIG_VALUE_") or key in (
            "GIT_CONFIG_COUNT", "GIT_CONFIG_PARAMETERS", "GIT_CONFIG", "GIT_DIR", "GIT_WORK_TREE",
            "SOTEA_TEST_HELPER_DELAY", "SOTEA_TEST_HELPER_EMPTY", "SOTEA_TEST_HELPER_USERNAME",
        ):
            env.pop(key)
    execute(["git", "init", "--quiet", str(directory / "repo")], env=env)
    helper = "!" + shlex.join([sys.executable, str(ROOT / "testdata/p0/credential-helper.py")])
    base = ["git", "-C", str(directory / "repo"), "-c", "credential.helper=", "-c", "credential.helper=" + helper]
    context = "protocol=https\nhost=git.sotea.invalid:8443\npath=team/a.git\nusername=p0-owner\n\n"
    for use_path in (False, True):
        output = execute([*base, "-c", f"credential.useHttpPath={str(use_path).lower()}", "credential", "fill"], env, context)
        if "password=sotea-synthetic-helper-password" not in output:
            raise RuntimeError("隔离 helper 未返回合成凭证")
    data = context.rstrip() + "\npassword=sotea-synthetic-helper-password\n\n"
    for operation in ("approve", "reject"):
        execute([*base, "credential", operation], env, data)
    events = [json.loads(line) for line in (directory / "helper.jsonl").read_text().splitlines()]
    if [event["operation"] for event in events] != ["get", "get", "store", "erase"]:
        raise RuntimeError("helper 协议操作不符合预期")
    if "path" in events[0] or events[1].get("path") != "team/a.git":
        raise RuntimeError("useHttpPath 夹具不符合预期")
    if "sotea-synthetic-helper-password" in (directory / "helper.jsonl").read_text():
        raise RuntimeError("helper 日志意外保存凭证")
    return {"operations": ["get", "get", "store", "erase"], "path_scope": True, "redacted_log": True}


def certificate(directory):
    """生成独立测试 CA 和带 loopback SAN 的服务端证书。

    所有私钥留在被忽略目录，并限制为当前用户可读。
    """
    directory.mkdir(exist_ok=True, mode=0o700)
    ca, key, cert = directory / "ca.crt", directory / "server.key", directory / "server.crt"
    execute(["openssl", "req", "-x509", "-newkey", "rsa:2048", "-nodes", "-days", "7",
             "-addext", "basicConstraints=critical,CA:TRUE", "-addext", "keyUsage=critical,keyCertSign,cRLSign",
             "-subj", "/CN=sotea P0 Test CA", "-keyout", str(directory / "ca.key"), "-out", str(ca)])
    execute(["openssl", "req", "-new", "-newkey", "rsa:2048", "-nodes", "-subj", "/CN=localhost",
             "-keyout", str(key), "-out", str(directory / "server.csr")])
    extension = directory / "server.ext"
    extension.write_text("basicConstraints=critical,CA:FALSE\nkeyUsage=critical,digitalSignature,keyEncipherment\n"
                         "subjectAltName=DNS:localhost,IP:127.0.0.1\nextendedKeyUsage=serverAuth\n"
                         "authorityKeyIdentifier=keyid,issuer\n")
    execute(["openssl", "x509", "-req", "-days", "7", "-in", str(directory / "server.csr"),
             "-CA", str(ca), "-CAkey", str(directory / "ca.key"), "-CAcreateserial",
             "-extfile", str(extension), "-out", str(cert)])
    for secret in (key, directory / "ca.key"):
        secret.chmod(0o600)
    return ca, key, cert


def http_checks(directory):
    """核对状态、超时、断连计数及受信任 CA 的行为。

    请求仅发往夹具的随机本机端口，不使用外部代理。
    """
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    with FixtureServer() as server:
        with opener.open(server.url + "/ok", timeout=2) as response:
            assert response.status == 200
        try:
            opener.open(server.url + "/status/403", timeout=2)
            raise RuntimeError("状态夹具未返回预期错误")
        except urllib.error.HTTPError as error:
            assert error.code == 403
        try:
            opener.open(server.url + "/delay?seconds=0.3", timeout=0.05)
            raise RuntimeError("延迟夹具未触发超时")
        except (TimeoutError, urllib.error.URLError):
            pass
        try:
            opener.open(urllib.request.Request(server.url + "/disconnect", data=b"synthetic", method="POST"), timeout=2)
            raise RuntimeError("断连夹具未中断请求")
        except (OSError, http.client.RemoteDisconnected):
            pass
        with opener.open(server.url + "/events", timeout=2) as response:
            events = json.load(response)
        assert sum(event["method"] == "POST" for event in events) == 1
    ca, key, cert = certificate(directory / "ca")
    with FixtureServer(str(cert), str(key)) as server:
        try:
            opener.open(server.url + "/ok", timeout=2)
            raise RuntimeError("未知 CA 被意外信任")
        except urllib.error.URLError as error:
            assert isinstance(error.reason, ssl.SSLCertVerificationError)
        trusted = urllib.request.build_opener(urllib.request.ProxyHandler({}),
            urllib.request.HTTPSHandler(context=ssl.create_default_context(cafile=str(ca))))
        with trusted.open(server.url + "/ok", timeout=2) as response:
            assert response.status == 200
    return {"status_403": True, "timeout": True, "disconnect_post_count": 1,
            "unknown_ca_rejected": True, "private_ca_accepted": True}


def main():
    """执行一次基础设施演练并保存非敏感结论。

    helper 临时数据自动清理，测试 CA 保留在受忽略目录供后续验证使用。
    """
    state = ROOT / "tests/integration/.state"
    state.mkdir(parents=True, exist_ok=True, mode=0o700)
    with tempfile.TemporaryDirectory(prefix="fixtures-", dir=state) as temporary:
        helper = helper_checks(Path(temporary))
    result = {"time_utc": datetime.now(timezone.utc).isoformat(), "status": "passed",
              "helper": helper, "http_tls": http_checks(state)}
    (state / "fixtures-result.json").write_text(json.dumps(result, ensure_ascii=False, indent=2) + "\n")
    print("P0 helper、HTTP 故障和私有 CA 演练通过；未读取真实凭证。")


if __name__ == "__main__":
    main()
