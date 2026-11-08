#!/usr/bin/env python3
"""通过真实 stdio 和七个固定 Gitea 实例验证 P6 代表性业务。

只使用 P0 合成账号；本轮随机资源按稳定 ID 清理，不接触开发者账号或生产实例。
"""

import argparse
import base64
from datetime import datetime, timezone
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import queue
import secrets
import subprocess
import tempfile
import threading
import time
import urllib.error
import urllib.request

ROOT = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location("p0", ROOT / "scripts/p0-environment.py")
fixture = importlib.util.module_from_spec(spec)
spec.loader.exec_module(fixture)


class Client:
    """管理一个隔离 MCP 子进程和真实逐行协议连接。

    队列读取有超时；关闭必须 EOF 自行退出，不把强制 kill 当清理成功。
    """

    def __init__(self, binary, configuration, environment, directory, protocol="2025-11-25", write=True, raw=True):
        """启动显式授权根和固定身份进程，不改全局客户端配置。

        工具权限由参数控制，协议 stdout 逐行解析且禁止密钥外泄。
        """
        args = [str(binary), "--config", str(configuration), "--workdir", str(directory), "--repo", environment["SOTEA_REPO"], "mcp", "serve"]
        if write:
            args.append("--allow-write")
        if raw:
            args.append("--allow-raw-api")
        self.process = subprocess.Popen(args, cwd=ROOT, env=environment, stdin=subprocess.PIPE,
                                        stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
        self.messages = queue.Queue()
        self.counter = 0
        self.secrets = [environment.get("SOTEA_TOKEN", "")]
        self.reader = threading.Thread(target=self.read_messages, daemon=True)
        self.reader.start()
        initialized = self.request("initialize", {"protocolVersion": protocol, "capabilities": {},
                                                   "clientInfo": {"name": "sotea-p6-verifier", "version": "1"}})
        assert initialized["protocolVersion"] == protocol
        self.process.stdin.write(json.dumps({"jsonrpc": "2.0", "method": "notifications/initialized"}) + "\n")
        self.process.stdin.flush()

    def read_messages(self):
        """读取全部协议行并交给请求方检查，不输出远端内容。

        EOF 发出哨兵，普通日志和非法 JSON 均由请求方报告失败。
        """
        for line in self.process.stdout:
            self.messages.put(line)
        self.messages.put(None)

    def request(self, method, params):
        """发送一个 JSON-RPC 请求，等待对应 ID 且保留工具业务错误。

        不接受无限等待；协议错误只报告稳定类别，不回显请求或凭证。
        """
        self.counter += 1
        self.process.stdin.write(json.dumps({"jsonrpc": "2.0", "id": self.counter, "method": method, "params": params}) + "\n")
        self.process.stdin.flush()
        while True:
            line = self.messages.get(timeout=90)
            if line is None:
                raise RuntimeError("MCP exited before completing a protocol request.")
            if any(secret and secret in line for secret in self.secrets):
                raise RuntimeError("A synthetic credential reached MCP stdout.")
            value = json.loads(line)
            if value.get("id") != self.counter:
                continue
            if "error" in value:
                raise RuntimeError(f"MCP protocol error {value['error']['code']} for {method}.")
            return value["result"]

    def tool(self, name, arguments=None, failure=None):
        """调用工具并核对结构化结果与完整 JSON 文本副本一致。

        只接受预期业务错误，超出最终输出预算或 ANSI 污染均失败。
        """
        result = self.request("tools/call", {"name": "sotea_" + name, "arguments": arguments or {}})
        envelope = result.get("structuredContent")
        if not isinstance(envelope, dict):
            raise RuntimeError(f"{name} lost structured content.")
        copies = [json.loads(item["text"]) for item in result.get("content", [])
                  if item.get("type") == "text" and item.get("text", "").startswith("{")]
        assert envelope in copies
        assert len(json.dumps(result, ensure_ascii=False).encode()) <= 256 << 10
        assert "\x1b" not in json.dumps(result)
        if failure:
            assert result.get("isError") and envelope.get("error", {}).get("code") == failure, (name, envelope.get("error"))
        elif result.get("isError"):
            error = envelope.get("error") or next((x.get("error") for x in envelope["items"] if x.get("error")), {})
            raise RuntimeError(f"{name}: {error.get('code')}: {error.get('message')}")
        return envelope

    def action(self, action, params, context=None):
        """准备并提交一次真实动作，重复提交后结果必须保持一致。

        写入前预览不截断，确认 token 只在本进程内存使用，不写入报告。
        """
        request_id = secrets.token_hex(12)
        prepared = self.tool("write_prepare", {"request_id": request_id, "action": action,
                                              "context": context or {}, "params": params})
        plan = prepared["meta"]["plan"]
        assert plan["committable"] and prepared["meta"]["execution"]["write_attempted"] == "no"
        args = {"plan_id": plan["plan_id"], "confirmation_token": plan["confirmation_token"]}
        committed = self.tool("write_commit", args)
        repeated = self.tool("write_commit", args)
        assert committed == repeated
        status = self.tool("write_status", {"request_id": request_id})
        assert status == committed and status["meta"]["plan"]["plan_state"] == "completed"
        assert "confirmation_token" not in status["meta"]["plan"]
        return committed

    def close(self):
        """关闭 stdin 验证 EOF 清理，并确认 stderr 没有业务日志或密钥。

        失败时才强制结束以避免遗留进程，此时不记录清理通过。
        """
        self.process.stdin.close()
        try:
            code = self.process.wait(timeout=15)
            self.reader.join(timeout=2)
            diagnostic = self.process.stderr.read()
            assert code == 0 and not self.reader.is_alive() and diagnostic == "", "MCP EOF cleanup failed."
        finally:
            if self.process.poll() is None:
                self.process.kill()
                self.process.wait(timeout=5)
            self.process.stdout.close()
            self.process.stderr.close()


def smoke(binary, version):
    """在单个精确实例创建本轮资源，完成目录／Issue／PR／Release／API 链路。

    只有准备环境使用直接 HTTP，产品业务通过 MCP 子进程完成；每个创建资源记录稳定 ID。
    """
    credentials = json.loads((fixture.STATE / f"credentials-{version}.json").read_text())
    bootstrap = json.loads((fixture.STATE / f"p4-bootstrap-{version}.json").read_text())
    credentials[bootstrap["username"]] = bootstrap["password"]
    url = f"http://127.0.0.1:{fixture.VERSIONS[version]}"
    prefix = "sotea-p6-" + secrets.token_hex(6)
    repo = "p0-owner/" + prefix
    org = prefix + "-org"
    repo_id = org_id = org_repo_id = None
    tokens = []
    clients = []
    cleanup = []
    checks = []
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), fixture.NoRedirect())

    def api(path, method="GET", payload=None, user="p0-owner"):
        """对固定本机端口进行有界夹具调用，不输出认证或服务端完整响应。

        清理前的 ID 核对也经过此入口，任何错误都保留为失败而非忽略。
        """
        authorization = base64.b64encode(f"{user}:{credentials[user]}".encode()).decode()
        request = urllib.request.Request(url + "/api/v1" + path, method=method,
                                         data=None if payload is None else json.dumps(payload).encode(),
                                         headers={"Authorization": "Basic " + authorization, "Content-Type": "application/json"})
        try:
            with opener.open(request, timeout=30) as response:
                raw = response.read(8 << 20)
        except urllib.error.HTTPError as error:
            raise RuntimeError(f"{version}: fixture {method} {path} returned HTTP {error.code}.") from None
        return json.loads(raw) if raw else None

    def first(envelope):
        """取得单资源数据，调用方仍需检查它的具体业务身份与字段。

        不使用标题或响应顺序猜测新资源编号。
        """
        assert envelope["items"]
        return envelope["items"][0]["data"]

    try:
        repository = api("/user/repos", "POST", {"name": prefix, "auto_init": True, "default_branch": "main"})
        repo_id = repository["id"]
        organization = api("/orgs", "POST", {"username": org, "visibility": "public"}, user=bootstrap["username"])
        org_id = organization["id"]
        owners = next(x for x in api("/orgs/" + org + "/teams", user=bootstrap["username"]) if x["name"] == "Owners")
        api(f"/teams/{owners['id']}/members/p0-owner", "PUT", user=bootstrap["username"])
        organization_repository = api("/orgs/" + org + "/repos", "POST",
                                      {"name": prefix, "auto_init": True, "default_branch": "main"}, user=bootstrap["username"])
        org_repo_id = organization_repository["id"]
        api("/repos/" + repo + "/collaborators/p0-reviewer", "PUT", {"permission": "write"})
        with tempfile.TemporaryDirectory(prefix="sotea-p6-") as temporary:
            directory = Path(temporary)
            configuration = directory / "config.json"
            instance = {"url": url, "proxy": "direct", "active_account": "p0-owner",
                        "accounts": {user: {"instance_url": url, "source": "environment"}
                                     for user in ("p0-owner", "p0-reviewer")}}
            configuration.write_text(json.dumps({"version": 1, "revision": 1, "instances": {"team": instance},
                                                 "repositories": []}))
            environment = dict(os.environ)
            for key in list(environment):
                if key.startswith("SOTEA_") or key.startswith("GIT_CONFIG"):
                    environment.pop(key)
            environment.update(CI="1", GIT_CONFIG_NOSYSTEM="1", GIT_CONFIG_GLOBAL=os.devnull,
                               GIT_CONFIG_COUNT="0", SOTEA_TOKEN_INSTANCE="team", SOTEA_INSTANCE="team",
                               SOTEA_REPO=repo, SOTEA_ACCOUNT="p0-owner")
            for user in ("p0-owner", "p0-reviewer"):
                token = api("/users/" + user + "/tokens", "POST", {"name": prefix + "-" + user, "scopes": ["all"]}, user)
                tokens.append((user, token["id"]))
                selected = dict(environment, SOTEA_TOKEN=token["sha1"], SOTEA_ACCOUNT=user)
                cli = subprocess.run([str(binary), "--config", str(configuration), "--instance", "team", "--account", user,
                                      "--repo", repo, "--json", "repo", "view"], env=selected, capture_output=True, timeout=30)
                assert cli.returncode == 0 and json.loads(cli.stdout)["items"][0]["data"]["id"] == repo_id
                client = Client(binary, configuration, selected, directory,
                                protocol="2025-11-25" if user == "p0-owner" else "2025-06-18")
                clients.append(client)
            owner, reviewer = clients
            assert len(owner.request("tools/list", {})["tools"]) == 32
            assert first(owner.tool("instance_info"))["raw_version"] == version
            assert first(owner.tool("auth_status", {"check": True}))["account"] == "p0-owner"
            owner.tool("context_get")
            assert any(x["id"] == repo_id for x in first(owner.tool("account_repo_list", {"limit": 100}))["items"])
            assert any(x["id"] == org_id for x in first(owner.tool("account_org_list", {"limit": 100}))["items"])
            org_items = first(owner.tool("org_repo_list", {"organization": org, "organization_id": org_id}))["items"]
            assert any(x["id"] == org_repo_id for x in org_items)
            owner.tool("repo_view", {"expected_repository_id": repo_id})
            for name in ("repo_assignee_list", "branch_list", "tag_list", "commit_list", "label_list", "milestone_list"):
                owner.tool(name)
            owner.tool("commit_status", {"ref": "main"})
            checks.append("both_protocols_32_tools_identity_catalog_common_reads_cli_parity")
            issue_result = owner.action("issue.create", {"title": prefix, "body": "MCP issue"})
            issue_number = first(issue_result)["issue"]["number"]
            owner.tool("issue_list", {"q": prefix})
            owner.tool("issue_view", {"index": issue_number})
            owner.action("issue.edit", {"index": issue_number, "changes": {"body": "edited", "milestone_id": 0}})
            owner.action("issue.comment.add", {"index": issue_number, "body": "MCP comment"})
            owner.tool("issue_comment_list", {"index": issue_number})
            label = api("/repos/" + repo + "/labels", "POST", {"name": prefix, "color": "#336699"})
            owner.action("issue.label.add", {"index": issue_number, "label_ids": [label["id"]]})
            owner.tool("issue_label_list", {"index": issue_number})
            owner.action("issue.label.remove", {"index": issue_number, "label_ids": [label["id"]]})
            owner.action("issue.assignee.add", {"index": issue_number, "users": ["p0-reviewer"]})
            owner.action("issue.assignee.remove", {"index": issue_number, "users": ["p0-reviewer"]})
            owner.action("issue.close", {"indices": [issue_number]})
            owner.action("issue.reopen", {"indices": [issue_number]})
            checks.append("issue_9_actions_readonly_prepare_single_commit_status_repeats")
            branch = prefix + "-branch"
            api("/repos/" + repo + "/branches", "POST", {"new_branch_name": branch, "old_branch_name": "main"})
            api("/repos/" + repo + "/contents/mcp.txt", "POST", {"branch": branch,
                "content": base64.b64encode(b"MCP integration\n").decode(), "message": "P6 isolated change"})
            created = owner.action("pr.create", {"base": "main", "head": branch, "title": prefix})
            pull = first(created)["resource"]
            number = pull["number"]
            owner.tool("pr_list")
            for name in ("pr_view", "pr_files", "pr_commits", "pr_comment_list", "pr_review_list"):
                owner.tool(name, {"index": number})
            owner.action("pr.edit", {"index": number, "changes": {"body": "MCP PR"}})
            owner.action("pr.comment.add", {"index": number, "body": "MCP PR comment"})
            owner.action("pr.reviewer.add", {"index": number, "users": ["p0-reviewer"]})
            reviewer.action("pr.review.create", {"index": number, "event": "approve"})
            owner.action("pr.close", {"indices": [number]})
            owner.action("pr.reopen", {"indices": [number]})
            # 状态转换与合并使用独立 PR，避免 Gitea 后台 close/reopen 队列尚未收敛。
            # 不重试已发送合并；只等待独立新 PR 的只读状态稳定。
            merge_branch = prefix + "-merge"
            api("/repos/" + repo + "/branches", "POST", {"new_branch_name": merge_branch, "old_branch_name": "main"})
            api("/repos/" + repo + "/contents/merge.txt", "POST", {"branch": merge_branch,
                "content": base64.b64encode(b"MCP isolated merge\\n").decode(), "message": "P6 merge change"})
            merge_created = owner.action("pr.create", {"base": "main", "head": merge_branch, "title": prefix + " merge"})
            merge_number = first(merge_created)["resource"]["number"]
            deadline = time.monotonic() + 30
            stable = 0
            previous = None
            while time.monotonic() < deadline:
                pull = first(owner.tool("pr_view", {"index": merge_number}))
                signature = (pull["head"]["sha"], pull["base"]["sha"], pull["state"], pull["mergeable"])
                stable = stable + 1 if signature == previous and pull["mergeable"] else 0
                if stable >= 10:
                    break
                previous = signature
                time.sleep(0.25)
            assert stable >= 10, "The isolated PR did not reach stable mergeability."
            owner.action("pr.merge", {"index": merge_number, "method": "merge", "delete_branch": False})
            checks.append("pr_8_actions_reviews_fixed_head_merge_and_reads")
            tag = "p6/" + prefix
            owner.action("release.create", {"tag": tag, "target": "main", "name": prefix})
            assert first(owner.tool("release_view", {"tag": tag}))["draft"] is True
            owner.tool("release_list")
            attachment = directory / "附件 sample.txt"
            attachment.write_text("P6 fixed attachment\n")
            owner.action("release.asset.upload", {"tag": tag, "file_path": attachment.name})
            assets = first(owner.tool("release_asset_list", {"tag": tag}))["items"]
            assert any(x["name"] == attachment.name for x in assets)
            owner.action("release.edit", {"tag": tag, "changes": {"draft": False, "prerelease": False}})
            assert first(owner.tool("release_view", {"tag": tag}))["draft"] is False
            checks.append("release_draft_tag_slash_fixed_file_upload_publish_reads")
            owner.tool("api_read", {"method": "GET", "path": "/repos/" + repo})
            owner.action("api.request", {"method": "PATCH", "path": "/repos/" + repo,
                                         "body_text": json.dumps({"description": "P6 raw API"})})
            assert api("/repos/" + repo)["description"] == "P6 raw API"
            checks.append("raw_read_write_plan_status_and_duplicate_commit")
            for client in clients:
                client.close()
            clients.clear()
            checks.append("eof_clean_exit_without_stdout_or_stderr_noise")
    finally:
        for client in clients:
            try:
                client.close()
            except Exception:
                cleanup.append("mcp_close_failed")
        for user, identifier in tokens:
            api(f"/users/{user}/tokens/{identifier}", "DELETE", user=user)
        if org_repo_id is not None:
            path = "/repos/" + org + "/" + prefix
            assert api(path, user=bootstrap["username"])["id"] == org_repo_id
            api(path, "DELETE", user=bootstrap["username"])
        if org_id is not None:
            assert api("/orgs/" + org, user=bootstrap["username"])["id"] == org_id
            api("/orgs/" + org, "DELETE", user=bootstrap["username"])
        if repo_id is not None:
            assert api("/repos/" + repo)["id"] == repo_id
            api("/repos/" + repo, "DELETE")
        assert not cleanup, cleanup
    return {"version": version, "status": "passed", "checks": checks, "cleanup": "verified_created_ids_and_tokens"}


def main():
    """逐版启动／验证／停止固定实例并保存与二进制摘要绑定的脱敏记录。

    失败保留当前版本与已通过结果，不能把部分版本成功写成七版本通过。
    """
    parser = argparse.ArgumentParser(description="Verify complete MCP tools against isolated Gitea versions.")
    parser.add_argument("--binary", type=Path, default=ROOT / "dist/p6/darwin-arm64/sotea")
    parser.add_argument("--version", action="append", choices=tuple(fixture.VERSIONS))
    args = parser.parse_args()
    binary = args.binary.resolve()
    record = {"schema_version": 1, "time_utc": datetime.now(timezone.utc).isoformat(),
              "binary_sha256": hashlib.sha256(binary.read_bytes()).hexdigest(), "versions": []}
    output = fixture.STATE / "p6-mcp-result.json"
    for version in args.version or fixture.VERSIONS:
        service = "gitea-" + version.replace(".", "-")
        try:
            fixture.start(version)
            record["versions"].append(smoke(binary, version))
            print(f"{version}: MCP representative workflows passed.", flush=True)
        except Exception as error:
            record["versions"].append({"version": version, "status": "failed", "error": str(error)})
            raise
        finally:
            fixture.compose("stop", service)
            fixture.compose("rm", "-f", service)
            output.write_text(json.dumps(record, indent=2) + "\n")
    print("P6 MCP workflows and scoped cleanup passed.", flush=True)


if __name__ == "__main__":
    main()
