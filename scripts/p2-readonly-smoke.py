#!/usr/bin/env python3
"""验证七个固定 Gitea 实例的 P2 认证生命周期与只读链路。

只使用 P0 合成账号和临时本地仓库；生成的测试 token 在结束时撤销。
"""

import argparse
import base64
from datetime import datetime, timezone
import hashlib
import json
import os
from pathlib import Path
import secrets
import shlex
import subprocess
import sys
import tempfile
import urllib.error
import urllib.request

ROOT = Path(__file__).resolve().parents[1]
STATE = ROOT / "tests/integration/.state"
VERSIONS = {"1.22.6": 32206, "1.23.8": 32308, "1.24.7": 32407, "1.25.5": 32505,
            "1.26.4": 32604, "1.27.3": 32703, "28.3.0": 32830}
REPOSITORY = "p0-owner/sotea-p0"


class NoRedirect(urllib.request.HTTPRedirectHandler):
    """避免测试凭证跟随重定向离开固定实例。

    所有测试请求仅允许清单中的 loopback 地址。
    """

    def redirect_request(self, request, file, code, message, headers, new_url):
        """阻止创建重定向目标请求。

        调用者按原 HTTP 状态处理，不能泄露认证到其他来源。
        """
        return None


def api(root_url, path, credentials, method="GET", payload=None):
    """为合成测试 token 执行固定实例的有界初始化或清理。

    失败只报告状态，不打印请求认证、响应 body 或密钥。
    """
    auth = base64.b64encode(("p0-owner:" + credentials["p0-owner"]).encode()).decode()
    request = urllib.request.Request(
        root_url + "/api/v1" + path,
        data=None if payload is None else json.dumps(payload).encode(),
        method=method, headers={"Authorization": "Basic " + auth, "Content-Type": "application/json"})
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), NoRedirect())
    try:
        with opener.open(request, timeout=20) as response:
            raw = response.read(1024 * 1024)
    except urllib.error.HTTPError as error:
        raise RuntimeError(f"Isolated token setup/cleanup returned HTTP {error.code}.") from None
    return json.loads(raw) if raw else None


def git(args, environment):
    """执行隔离仓库的 Git 准备或只读远程获取。

    参数与失败输出不复制到诊断，环境关闭系统和全局配置。
    """
    result = subprocess.run(["git", *args], env=environment, cwd=ROOT,
                            capture_output=True, text=True, timeout=30)
    if result.returncode:
        raise RuntimeError("Isolated Git setup failed.")
    return result.stdout.strip()


def smoke(binary, version, system_storage):
    """从仓库发现到退出后跨进程验证完整链路。

    每次 sotea 调用均为新进程；只写独立配置和新建系统凭证引用。
    """
    credentials = json.loads((STATE / f"credentials-{version}.json").read_text())
    root_url = f"http://127.0.0.1:{VERSIONS[version]}"
    checks = []
    token_id = None
    with tempfile.TemporaryDirectory(prefix="sotea-p2-") as temporary:
        directory = Path(temporary)
        worktree = directory / "中文 repository"
        worktree.mkdir()
        configuration = directory / "config.json"
        helper_log = directory / "helper-events.jsonl"
        environment = dict(os.environ)
        for name in ("SOTEA_TOKEN", "SOTEA_TOKEN_INSTANCE", "SOTEA_INSTANCE", "SOTEA_ACCOUNT", "SOTEA_CONFIG"):
            environment.pop(name, None)
        environment.update(
            GIT_CONFIG_NOSYSTEM="1", GIT_CONFIG_GLOBAL=os.devnull, GIT_CONFIG_COUNT="0",
            GIT_TERMINAL_PROMPT="0", SOTEA_P2_CREDENTIALS=json.dumps(credentials),
            SOTEA_P2_HELPER_USER="p0-owner", SOTEA_P2_HELPER_LOG=str(helper_log), CI="1")
        helper = "!" + shlex.quote(sys.executable) + " " + shlex.quote(str(ROOT / "testdata/p2/credential-helper.py"))
        git(["-C", str(worktree), "init", "--quiet", "--initial-branch=main"], environment)
        git(["-C", str(worktree), "remote", "add", "team", root_url + "/" + REPOSITORY + ".git"], environment)
        git(["-C", str(worktree), "config", "credential.helper", ""], environment)
        git(["-C", str(worktree), "config", "--add", "credential.helper", helper], environment)
        git(["-C", str(worktree), "config", "credential.useHttpPath", "true"], environment)
        git(["-C", str(worktree), "fetch", "--quiet", "--depth=1", "team", "main"], environment)
        git(["-C", str(worktree), "checkout", "--quiet", "-B", "main", "FETCH_HEAD"], environment)

        def command(args, code=0, secret=None, raw=False, cwd=None):
            """运行独立 sotea 进程并验证结构，不输出密钥或完整响应。

            特定失败必须匹配预期退出码；业务 JSON 必须独立可解析。
            """
            base = [str(binary), "--config", str(configuration), "--workdir", str(cwd or worktree),
                    "--no-interactive"]
            if not raw:
                base.append("--json")
            result = subprocess.run([*base, *args], env=environment, cwd=ROOT,
                                    input=secret, capture_output=True, text=True, timeout=30)
            if result.returncode != code:
                category = "unparsed"
                try:
                    category = json.loads(result.stdout).get("error", {}).get("code")
                except (ValueError, AttributeError):
                    pass
                raise RuntimeError(f"{version}: {args[0]} returned {result.returncode}; expected {code}; category={category}.")
            for value in credentials.values():
                if value in result.stdout or value in result.stderr:
                    raise RuntimeError("A synthetic credential reached process output.")
            return result.stdout if raw else json.loads(result.stdout)

        try:
            command(["auth", "login", "--credential-source", "git", "--name", "team", "--confirm-instance"])
            saved = json.loads(configuration.read_text())
            assert saved["instances"]["team"]["accounts"]["p0-owner"]["source"] == "git"
            checks.append("anonymous_detection_and_confirmed_helper_login")
            command(["auth", "status", "--check"])
            version_result = command(["instance", "info", "--refresh"])
            assert version_result["items"][0]["data"]["raw_version"] == version
            assert version_result["items"][0]["data"]["instance_evidence"] == "not_tested"
            checks.append("identity_and_exact_version_evidence")
            for args in (["repo", "view"], ["branch", "list", "--limit", "2", "--all"],
                         ["tag", "list"], ["commit", "list", "--limit", "2"],
                         ["commit", "status"], ["label", "list"], ["milestone", "list"]):
                command(args)
            checks.append("all_seven_repository_reads")
            environment["SOTEA_P2_HELPER_USER"] = "p0-reviewer"
            command(["auth", "login", "--credential-source", "git", "--account", "p0-reviewer"])
            command(["auth", "switch", "p0-owner"])
            environment["SOTEA_P2_HELPER_USER"] = "p0-owner"
            command(["auth", "refresh"])
            checks.append("multi_account_switch_and_helper_refresh")
            before = len(helper_log.read_text().splitlines())
            command(["auth", "logout", "--yes"])
            command(["repo", "view"], code=1)
            assert len(helper_log.read_text().splitlines()) == before
            checks.append("logout_suppression_across_processes")
            command(["auth", "login", "--credential-source", "git", "--account", "p0-owner"])
            command(["auth", "logout", "--forget-git", "--yes", "--confirm-forget-git"])
            events = [json.loads(line) for line in helper_log.read_text().splitlines()]
            assert events[-1]["operation"] == "erase"
            checks.append("separate_shared_helper_rejection")
            command(["auth", "login", "--credential-source", "git", "--account", "p0-owner"])
            token = api(root_url, "/users/p0-owner/tokens", credentials, "POST",
                        {"name": "sotea-p2-" + secrets.token_hex(8), "scopes": ["all"]})
            token_id = token["id"]
            environment["SOTEA_TOKEN"] = token["sha1"]
            environment["SOTEA_TOKEN_INSTANCE"] = "team"
            command(["auth", "login", "--credential-source", "environment"])
            command(["--instance", "team", "--repo", REPOSITORY, "repo", "view"], cwd=directory)
            command(["auth", "logout", "--yes"])
            command(["repo", "view"])
            status = command(["auth", "status"])
            assert status["items"][0]["data"]["environment_present"]
            checks.append("environment_token_scope_logout_notice_and_no_git_directory")
            raw = command(["api", "/repos/" + REPOSITORY], raw=True)
            assert json.loads(raw)["full_name"] == REPOSITORY
            checks.append("raw_authenticated_api")
            environment.pop("SOTEA_TOKEN")
            environment.pop("SOTEA_TOKEN_INSTANCE")
            if system_storage:
                command(["auth", "login", "--credential-source", "token", "--with-token",
                         "--account", "p0-owner"], secret=token["sha1"])
                command(["auth", "status", "--check"])
                command(["auth", "refresh", "--with-token"], secret=token["sha1"])
                configuration_text = configuration.read_text()
                assert token["sha1"] not in configuration_text
                command(["auth", "logout", "--yes"])
                checks.append("system_stored_token_login_refresh_and_removal")
            command(["auth", "login", "--credential-source", "git", "--account", "p0-owner"])
            command(["auth", "logout", "--yes"])
        finally:
            # 只删除当前临时配置中仍有引用的随机 sotea 项，避免失败后遗留合成 token。
            if system_storage and configuration.exists():
                saved = json.loads(configuration.read_text())
                environment.pop("SOTEA_TOKEN", None)
                environment.pop("SOTEA_TOKEN_INSTANCE", None)
                for name, instance in saved.get("instances", {}).items():
                    for account, value in instance.get("accounts", {}).items():
                        if value.get("credential_ref"):
                            command(["--instance", name, "--account", account, "auth", "logout", "--yes"])
            if token_id is not None:
                api(root_url, f"/users/p0-owner/tokens/{token_id}", credentials, "DELETE")
    return {"version": version, "status": "passed", "checks": checks}


def main():
    """解析固定版本清单并写入不含凭证的结果记录。

    不启动或重置 Gitea 服务，执行前应已按 P0 文档启动测试实例。
    """
    parser = argparse.ArgumentParser(description="Verify P2 against fixed isolated Gitea instances.")
    parser.add_argument("--binary", type=Path, default=ROOT / "dist/p2/darwin-arm64/sotea")
    parser.add_argument("--versions", nargs="+", choices=VERSIONS, default=list(VERSIONS))
    parser.add_argument("--system-storage", action="store_true", help="Use new synthetic system credential entries and remove them")
    args = parser.parse_args()
    binary = args.binary.resolve()
    record = {"schema_version": 1, "time_utc": datetime.now(timezone.utc).isoformat(),
              "binary_sha256": hashlib.sha256(binary.read_bytes()).hexdigest(),
              "system_storage_requested": args.system_storage, "versions": []}
    try:
        for version in args.versions:
            result = smoke(binary, version, args.system_storage)
            record["versions"].append(result)
            print(f"{version}: P2 isolated lifecycle and reads passed.", flush=True)
    finally:
        STATE.mkdir(parents=True, exist_ok=True)
        (STATE / "p2-readonly-result.json").write_text(json.dumps(record, indent=2) + "\n")


if __name__ == "__main__":
    try:
        main()
    except (RuntimeError, AssertionError, OSError, subprocess.TimeoutExpired) as error:
        if isinstance(error, RuntimeError):
            sys.exit(str(error))
        sys.exit("P2 isolated validation failed; inspect the scoped test environment.")
