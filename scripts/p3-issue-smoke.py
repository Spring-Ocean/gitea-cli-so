#!/usr/bin/env python3
"""验证七个固定 Gitea 实例的 Issue 写入、增量适配和原始文件传输。

只在 P0 仓库创建随机前缀资源，结束时按 ID／精确前缀清理，不重置仓库。
"""

import argparse
import base64
from datetime import datetime, timezone
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import secrets
import shlex
import subprocess
import sys
import tempfile
import traceback

ROOT = Path(__file__).resolve().parents[1]
module_spec = importlib.util.spec_from_file_location("p2_fixture", ROOT / "scripts/p2-readonly-smoke.py")
fixture = importlib.util.module_from_spec(module_spec)
module_spec.loader.exec_module(fixture)
REPOSITORY = fixture.REPOSITORY
BASE = "/repos/" + REPOSITORY


def smoke(binary, version):
    """运行一个精确版本的 Issue 完整链路，写入均通过最终 CLI 确认路径。

    直接 HTTP 只用于准备标签／里程碑／分支和边界内清理，合成密码不进入输出。
    """
    credentials = json.loads((fixture.STATE / f"credentials-{version}.json").read_text())
    root_url = f"http://127.0.0.1:{fixture.VERSIONS[version]}"
    prefix = "sotea-p3-" + secrets.token_hex(8)
    labels, milestones, assets = [], [], []
    branch_created = False
    checks = []
    with tempfile.TemporaryDirectory(prefix="sotea-p3-") as temporary:
        directory = Path(temporary)
        repository = directory / "中文 repository"
        repository.mkdir()
        configuration = directory / "config.json"
        environment = dict(os.environ)
        for name in ("SOTEA_TOKEN", "SOTEA_TOKEN_INSTANCE", "SOTEA_INSTANCE", "SOTEA_ACCOUNT", "SOTEA_CONFIG"):
            environment.pop(name, None)
        environment.update(GIT_CONFIG_NOSYSTEM="1", GIT_CONFIG_GLOBAL=os.devnull, GIT_CONFIG_COUNT="0",
                           SOTEA_P2_CREDENTIALS=json.dumps(credentials), SOTEA_P2_HELPER_USER="p0-owner", CI="1")
        helper = "!" + shlex.quote(sys.executable) + " " + shlex.quote(str(ROOT / "testdata/p2/credential-helper.py"))
        for args in (["init", "--quiet", "--initial-branch=main"],
                     ["remote", "add", "team", root_url + "/" + REPOSITORY + ".git"],
                     ["config", "credential.helper", ""], ["config", "--add", "credential.helper", helper],
                     ["config", "credential.useHttpPath", "true"]):
            fixture.git(["-C", str(repository), *args], environment)

        def api(path, method="GET", payload=None):
            """将所有准备与清理限定到当前固定实例和仓库资源。

            底层沿用禁止重定向、固定合成 owner 与有限响应。
            """
            try:
                return fixture.api(root_url, path, credentials, method, payload)
            except RuntimeError as error:
                raise RuntimeError(f"{version}: fixture {method} {path}: {error}") from None

        def run(args, code=0, raw=False):
            """运行独立 CLI 进程并检查退出码和可解析业务结果。

            失败诊断只包含操作类别，不打印密钥、原始请求或完整响应。
            """
            base = [str(binary), "--config", str(configuration), "--workdir", str(repository), "--no-interactive"]
            if not raw:
                base.append("--json")
            result = subprocess.run([*base, *args], env=environment, cwd=ROOT,
                                    capture_output=True, timeout=60)
            if result.returncode != code:
                category = "unparsed"
                try:
                    envelope = json.loads(result.stdout)
                    failure = envelope.get("error") or next((item.get("error") for item in envelope.get("items", []) if item.get("error")), {})
                    category = failure.get("code", "unparsed")
                except (ValueError, AttributeError):
                    pass
                raise RuntimeError(f"{version}: {' '.join(args[:2])} returned {result.returncode}; expected {code}; category={category}.")
            for secret in credentials.values():
                if secret.encode() in result.stdout or secret.encode() in result.stderr:
                    raise RuntimeError("A synthetic credential reached process output.")
            return (result.stdout if raw else json.loads(result.stdout)), result.stderr.decode(errors="replace")

        def write(args, code=0):
            """对业务写入始终显式提供 --yes，不通过脚本输入绕过确认。

            返回的逐项结果用于验证状态和多步骤执行证据。
            """
            return run([*args, "--yes"], code=code)

        try:
            run(["auth", "login", "--credential-source", "git", "--name", "team", "--confirm-instance"])
            for suffix, color in (("bug", "ee0000"), ("help", "00ee00")):
                labels.append(api(BASE + "/labels", "POST", {"name": prefix + "-" + suffix, "color": color}))
            milestones.append(api(BASE + "/milestones", "POST", {"title": prefix + "-milestone"}))
            body_file = directory / "body.txt"
            body = "P3 body\n中文内容\n"
            body_file.write_text(body)
            created, _ = write(["issue", "create", "--title", prefix + "-issue",
                                "--body-file", str(body_file), "--assignee", "p0-owner",
                                "--label", str(labels[0]["id"]), "--milestone", str(milestones[0]["id"])])
            number = created["items"][0]["data"]["issue"]["number"]
            viewed, _ = run(["issue", "view", str(number)])
            assert viewed["items"][0]["data"]["body"] == body
            listed, _ = run(["issue", "list", "--state", "all", "--search", prefix,
                            "--label", labels[0]["name"], "--assignee", "p0-owner",
                            "--milestone", str(milestones[0]["id"]), "--limit", "2", "--all"])
            if [value["number"] for value in listed["items"][0]["data"]["items"]] != [number]:
                current = api(BASE + f"/issues/{number}")
                detail = {"requested": number, "returned": [value["number"] for value in listed["items"][0]["data"]["items"]],
                          "actual_assignees": [value["login"] for value in current["assignees"]],
                          "actual_labels": [value["id"] for value in current["labels"]],
                          "actual_milestone": current["milestone"]["id"] if current["milestone"] else None}
                for key, value in (("--search", prefix), ("--label", labels[0]["name"]),
                                   ("--label", str(labels[0]["id"])), ("--assignee", "p0-owner"),
                                   ("--milestone", str(milestones[0]["id"]))):
                    sample, _ = run(["issue", "list", "--state", "all", key, value, "--all"])
                    detail[key + ("-id" if value == str(labels[0]["id"]) else "")] = [item["number"] for item in sample["items"][0]["data"]["items"]]
                raise RuntimeError("Common filter combination mismatch: " + json.dumps(detail))
            checks.append("create_view_and_all_common_filters")
            print(version + ": create and filters passed.", flush=True)
            write(["issue", "comment", "add", str(number), "--body", "P3 comment"])
            comments, _ = run(["issue", "comment", "list", str(number), "--all"])
            assert any(value["body"] == "P3 comment" for value in comments["items"][0]["data"]["items"])
            write(["issue", "edit", str(number), "--clear-body", "--clear-assignees", "--clear-labels", "--clear-milestone"])
            cleared = api(BASE + f"/issues/{number}")
            assert cleared["body"] == "" and not cleared["assignees"] and not cleared["labels"] and cleared["milestone"] is None
            write(["issue", "edit", str(number), "--title", prefix + "-edited", "--body", "edited",
                   "--assignee", "p0-owner", "--label", labels[0]["name"], "--milestone", milestones[0]["title"]])
            checks.append("comments_edit_and_explicit_clears")
            print(version + ": comments and field clears passed.", flush=True)
            write(["issue", "label", "add", str(number), "--label", str(labels[1]["id"])])
            label_values, _ = run(["issue", "label", "list", str(number)])
            assert {value["id"] for value in label_values["items"][0]["data"]} == {value["id"] for value in labels}
            write(["issue", "label", "remove", str(number), "--label", str(labels[0]["id"])])
            skipped, _ = write(["issue", "label", "add", str(number), "--label", str(labels[1]["id"])])
            assert skipped["items"][0]["status"] == "skipped"
            added, preview = write(["issue", "assignee", "add", str(number), "--user", "p0-reviewer"])
            legacy = version not in ("1.27.3", "28.3.0")
            assert ("requires replacing the full assignee set" in preview) == legacy
            current = api(BASE + f"/issues/{number}")
            assert {user["login"] for user in current["assignees"]} == {"p0-owner", "p0-reviewer"}
            write(["issue", "assignee", "remove", str(number), "--user", "p0-owner"])
            current = api(BASE + f"/issues/{number}")
            assert [user["login"] for user in current["assignees"]] == ["p0-reviewer"]
            checks.append("incremental_labels_and_versioned_assignees")
            print(version + ": collection deltas passed.", flush=True)
            write(["issue", "close", str(number), str(number)])
            skipped, _ = write(["issue", "close", str(number)])
            assert skipped["items"][0]["status"] == "skipped"
            write(["issue", "reopen", str(number)])
            second, _ = write(["issue", "create", "--title", prefix + "-second", "--label", str(labels[0]["id"])])
            other = second["items"][0]["data"]["issue"]["number"]
            for search in ([], ["--search", prefix]):
                matched, _ = run(["issue", "list", "--state", "all", "--label", str(labels[0]["id"]),
                                  "--label", str(labels[1]["id"]), "--all", *search])
                assert {value["number"] for value in matched["items"][0]["data"]["items"]} == {number, other}
            checks.append("multiple_labels_any_match_with_and_without_search")
            batch, _ = write(["issue", "close", str(number), "999999999", str(other)], code=3)
            assert [item["status"] for item in batch["items"]] == ["success", "failed", "success"]
            pulls = api(BASE + "/pulls?state=all&limit=1")
            if not pulls:
                raise RuntimeError("The isolated PR type-check fixture is missing.")
            rejected, _ = write(["issue", "close", str(pulls[0]["number"])], code=2)
            assert rejected["items"][0]["error"]["code"] == "RESOURCE_TYPE_MISMATCH"
            run(["issue", "reopen", str(number)], code=2)
            run(["issue", "close", "--yes"], code=2)
            write(["issue", "edit", str(number), "--body", "conflict", "--clear-body"], code=2)
            assert api(BASE + f"/issues/{number}")["state"] == "closed"
            checks.append("batch_dedup_skipped_partial_pr_and_confirmation_guards")
            print(version + ": batch and guards passed.", flush=True)
            raw, _ = run(["api", BASE + "/issues", "--method", "POST", "--data",
                          json.dumps({"title": prefix + "-raw", "body": "raw JSON preserved"}), "--yes"], raw=True)
            assert json.loads(raw)["title"] == prefix + "-raw"
            _, diagnostic = run(["api", BASE + "/issues", "--method", "POST", "--data",
                                 json.dumps({"title": prefix + "-limited-response"}),
                                 "--max-response-bytes", "1", "--yes"], code=3, raw=True)
            assert "PARTIAL_FAILURE" in diagnostic
            checks.append("accepted_raw_write_response_limit_retains_outcome")
            branch = api(BASE + "/branches", "POST", {"new_branch_name": prefix, "old_branch_name": "main"})
            branch_created = True
            binary_data = bytes(range(256)) * 1024
            file_request = directory / "raw-file.json"
            file_request.write_text(json.dumps({"branch": prefix, "content": base64.b64encode(binary_data).decode(),
                                                "message": "P3 isolated binary fixture"}))
            run(["api", BASE + "/contents/" + prefix + ".bin", "--method", "POST", "--input", str(file_request),
                 "--header", "Content-Type: application/json", "--yes"], raw=True)
            downloaded = directory / "download.bin"
            run(["api", BASE + "/media/" + prefix + ".bin", "--query", "ref=" + prefix, "--output", str(downloaded)], raw=True)
            assert downloaded.read_bytes() == binary_data
            run(["api", BASE + "/media/" + prefix + ".bin", "--query", "ref=" + prefix, "--output", str(downloaded)], code=1, raw=True)
            source = directory / (prefix + "-asset.bin")
            source.write_bytes(binary_data)
            releases = api(BASE + "/releases")
            release_id = releases[0]["id"]
            raw, _ = run(["api", BASE + f"/releases/{release_id}/assets", "--method", "POST",
                          "--upload", "attachment=" + str(source), "--yes"], raw=True)
            attachment = json.loads(raw)
            assets.append((release_id, attachment["id"]))
            assert attachment["name"] == source.name and attachment["size"] == len(binary_data)
            checks.append("raw_json_input_binary_download_no_replace_and_streamed_multipart")
            run(["auth", "logout", "--yes"])
        finally:
            # 不确定创建结果也通过随机精确前缀查询，只清理本轮新资源。
            issues = []
            page = 1
            while True:
                current_page = api(BASE + f"/issues?state=all&type=issues&limit=100&page={page}")
                if not current_page:
                    break
                issues.extend(current_page)
                if len(issues) > 10000:
                    raise RuntimeError("P3 cleanup exceeded its bounded resource scan.")
                page += 1
            for value in issues:
                if value["title"].startswith(prefix) and not value.get("pull_request"):
                    api(BASE + f"/issues/{value['number']}", "DELETE")
            for release_id, asset_id in assets:
                api(BASE + f"/releases/{release_id}/assets/{asset_id}", "DELETE")
            for value in milestones:
                api(BASE + f"/milestones/{value['id']}", "DELETE")
            for value in labels:
                api(BASE + f"/labels/{value['id']}", "DELETE")
            if branch_created:
                api(BASE + "/branches/" + prefix, "DELETE")
    return {"version": version, "status": "passed", "assignee_strategy": "read_modify_write" if legacy else "native_delta",
            "checks": checks, "cleanup": "passed"}


def main():
    """运行固定版本清单并归档不含凭证的 P3 结果。

    失败保留已通过版本记录，不能用模拟服务代替真实七版本结果。
    """
    parser = argparse.ArgumentParser(description="Verify P3 Issue operations in fixed isolated Gitea instances.")
    parser.add_argument("--binary", type=Path, default=ROOT / "dist/p3/darwin-arm64/sotea")
    parser.add_argument("--versions", nargs="+", choices=fixture.VERSIONS, default=list(fixture.VERSIONS))
    args = parser.parse_args()
    binary = args.binary.resolve()
    record = {"schema_version": 1, "time_utc": datetime.now(timezone.utc).isoformat(),
              "binary_sha256": hashlib.sha256(binary.read_bytes()).hexdigest(), "versions": []}
    try:
        for version in args.versions:
            record["versions"].append(smoke(binary, version))
            print(version + ": P3 Issue and raw file-transfer checks passed.", flush=True)
    finally:
        (fixture.STATE / "p3-issue-result.json").write_text(json.dumps(record, indent=2) + "\n")


if __name__ == "__main__":
    try:
        main()
    except (RuntimeError, AssertionError, OSError, subprocess.TimeoutExpired) as error:
        if isinstance(error, RuntimeError):
            sys.exit(str(error))
        frame=traceback.extract_tb(error.__traceback__)[-1]
        sys.exit(f"P3 isolated validation failed at {Path(frame.filename).name}:{frame.lineno} ({type(error).__name__}).")
