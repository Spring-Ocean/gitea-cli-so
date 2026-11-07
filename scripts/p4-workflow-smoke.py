#!/usr/bin/env python3
"""使用隔离组织／仓库验证七个精确版本的专用 PR 和 Release 命令。

测试账号来自既有 P0 合成凭证；结束只删除本轮随机命名资源，不修改 P0 原仓库。
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
import time
import traceback
import urllib.error
import urllib.request

ROOT = Path(__file__).resolve().parents[1]
module_spec = importlib.util.spec_from_file_location("p2_fixture", ROOT / "scripts/p2-readonly-smoke.py")
fixture = importlib.util.module_from_spec(module_spec)
module_spec.loader.exec_module(fixture)


def smoke(binary, version):
    """用最终 CLI 完成 PR 讨论／审查／合并以及 Release 草稿／附件／发布。

    API 只承担临时组织、分支和保护规则等准备，业务成功不能以手工 API 替代。
    """
    credentials = json.loads((fixture.STATE / f"credentials-{version}.json").read_text())
    root_url = f"http://127.0.0.1:{fixture.VERSIONS[version]}"
    prefix = "sotea-p4-" + secrets.token_hex(6)
    organization = prefix + "-org"
    repository = organization + "/" + prefix
    base = "/repos/" + repository
    organization_created = False
    repository_created = False
    fork_created = False
    checks = []
    permission_denials = []
    setup_user = "p4-bootstrap"
    setup_path = fixture.STATE / f"p4-bootstrap-{version}.json"
    if setup_path.exists():
        setup_secret = json.loads(setup_path.read_text())["password"]
    else:
        setup_secret = secrets.token_urlsafe(24)
        setup_path.write_text(json.dumps({"username": setup_user, "password": setup_secret}) + "\n")
        setup_path.chmod(0o600)
    credentials[setup_user] = setup_secret
    service = "gitea-" + version.replace(".", "-")
    container = subprocess.check_output(["docker", "compose", "-p", "sotea-p0", "-f", str(ROOT / "tests/integration/compose.yaml"), "ps", "-q", service], text=True).strip()

    def api(path, method="GET", payload=None, user="p0-owner", expected=None):
        """只访问当前固定 loopback 实例，认证头及响应不进入共享日志。

        expected 可验证故障状态；其他非成功状态只报告方法和路径，不复制凭证。
        """
        authorization = base64.b64encode((user + ":" + credentials[user]).encode()).decode()
        request = urllib.request.Request(root_url + "/api/v1" + path, method=method,
                                         data=None if payload is None else json.dumps(payload).encode(),
                                         headers={"Authorization": "Basic " + authorization, "Content-Type": "application/json"})
        opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), fixture.NoRedirect())
        try:
            with opener.open(request, timeout=30) as response:
                raw = response.read(8 << 20)
        except urllib.error.HTTPError as error:
            if expected == error.code:
                return None
            raise RuntimeError(f"{version}: fixture {method} {path} returned HTTP {error.code}.") from None
        if expected is not None:
            raise RuntimeError(f"{version}: expected HTTP {expected} for fixture {method} {path}.")
        return json.loads(raw) if raw else None

    try:
        users = subprocess.check_output(["docker", "exec", "--user", "git", container, "gitea", "--config", "/data/gitea/conf/app.ini", "admin", "user", "list"], text=True)
        if setup_user not in users.split():
            created = subprocess.run(["docker", "exec", "--user", "git", container, "gitea", "--config", "/data/gitea/conf/app.ini", "admin", "user", "create", "--username", setup_user, "--password", credentials[setup_user], "--email", setup_user + "@sotea.invalid", "--admin", "--must-change-password=false"], capture_output=True)
            if created.returncode:
                raise RuntimeError("Unable to create the scoped P4 setup identity.")
        api("/orgs", "POST", {"username": organization, "full_name": prefix, "visibility": "public"}, user=setup_user)
        organization_created = True
        owners = next(team for team in api("/orgs/" + organization + "/teams", user=setup_user) if team["name"] == "Owners")
        api(f"/teams/{owners['id']}/members/p0-owner", "PUT", user=setup_user)
        api("/orgs/" + organization + "/repos", "POST", {"name": prefix, "auto_init": True, "default_branch": "main"})
        repository_created = True
        team = api("/orgs/" + organization + "/teams", "POST", {"name": "Reviewers", "permission": "write", "units": ["repo.code", "repo.pulls"], "includes_all_repositories": True})
        api(f"/teams/{team['id']}/members/p0-reviewer", "PUT")
        api(base + "/collaborators/p0-limited", "PUT", {"permission": "read"})
        with tempfile.TemporaryDirectory(prefix="sotea-p4-") as temporary:
            directory = Path(temporary)
            worktree = directory / "中文 repository"
            worktree.mkdir()
            configuration = directory / "config.json"
            environment = dict(os.environ)
            for name in ("SOTEA_TOKEN", "SOTEA_TOKEN_INSTANCE", "SOTEA_INSTANCE", "SOTEA_ACCOUNT", "SOTEA_CONFIG"):
                environment.pop(name, None)
            environment.update(GIT_CONFIG_NOSYSTEM="1", GIT_CONFIG_GLOBAL=os.devnull, GIT_CONFIG_COUNT="0", CI="1",
                               SOTEA_P2_CREDENTIALS=json.dumps(credentials), SOTEA_P2_HELPER_USER="p0-owner")
            helper = "!" + shlex.quote(sys.executable) + " " + shlex.quote(str(ROOT / "testdata/p2/credential-helper.py"))
            for args in (["init", "--quiet", "--initial-branch=main"], ["remote", "add", "team", root_url + "/" + repository + ".git"],
                         ["config", "credential.helper", ""], ["config", "--add", "credential.helper", helper], ["config", "credential.useHttpPath", "true"]):
                fixture.git(["-C", str(worktree), *args], environment)

            def run(args, code=0, user="p0-owner"):
                """每条命令运行独立进程，通过实际专用命令检查结果和退出码。

                --account 固定 API 身份，失败仅报告稳定错误和安全摘要。
                """
                env = dict(environment, SOTEA_P2_HELPER_USER=user)
                command = [str(binary), "--config", str(configuration), "--workdir", str(worktree), "--account", user, "--json", "--no-interactive", *args]
                result = subprocess.run(command, cwd=ROOT, env=env, capture_output=True, timeout=90)
                for secret in credentials.values():
                    if secret.encode() in result.stdout or secret.encode() in result.stderr:
                        raise RuntimeError("A synthetic credential reached process output.")
                try:
                    envelope = json.loads(result.stdout)
                except ValueError:
                    raise RuntimeError(f"{version}: {' '.join(args[:2])} did not return business JSON.") from None
                if result.returncode != code:
                    failure = envelope.get("error") or next((item.get("error") for item in envelope.get("items", []) if item.get("error")), {})
                    detail = " ".join(args) if args[:2] == ["pr", "merge"] else " ".join(args[:2])
                    raise RuntimeError(f"{version}: {detail} as {user} returned {result.returncode}; expected {code}; {failure.get('code')}: {failure.get('message')}.")
                return envelope

            def write(args, code=0, user="p0-owner"):
                """所有业务写入明确提供 --yes，不能靠脚本输入猜测确认。

                只跳最终确认，缺参数仍应按命令契约报错且零业务写入。
                """
                return run([*args, "--yes"], code, user)

            def branch(name, repository_base=base, user="p0-owner", base_branch="main"):
                """准备一个有真实差异的远端分支，供 PR 专用创建命令使用。

                测试准备不调用 CLI push，也不把未提交本地内容当作远端分支。
                """
                api(repository_base + "/branches", "POST", {"new_branch_name": name, "old_branch_name": base_branch}, user)
                api(repository_base + "/contents/" + name + ".txt", "POST", {"branch": name, "content": base64.b64encode((name + "\n").encode()).decode(), "message": "P4 isolated change"}, user)


            def fixture_pull_state(number):
                """只读查询本轮仓库 PR 的后台检查状态，区分 checking 与 conflict。

                官方 API 的 mergeable 布尔会把其他状态折叠；数据库只用于测试准备核验，不供产品调用。
                """
                assert number.isdecimal() and repository_created
                repository_id = api(base)["id"]
                query = f'SELECT status, conflicted_files FROM pull_request WHERE base_repo_id={int(repository_id)} AND "index"={int(number)};'
                raw = subprocess.check_output(["docker", "exec", container, "sqlite3", "-readonly", "-cmd", ".timeout 3000", "/data/gitea/gitea.db", query], text=True).strip()
                status, _, conflicted_files = raw.partition("|")
                return status, conflicted_files

            def wait_mergeable(number):
                """有界等待合成分支的异步差异检查完成，只重查读取。

                不重放合并；冲突和保护场景不使用此等待伪装可合并。
                """
                deadline = time.monotonic() + 10
                stable = 0
                previous = None
                while time.monotonic() < deadline:
                    current = api(base + "/pulls/" + str(number))
                    signature = (current["head"]["sha"], current["base"]["sha"], current["state"], current["mergeable"])
                    stable = stable + 1 if signature == previous and current["mergeable"] and fixture_pull_state(number)[0] == "2" else 0
                    if stable >= 10:
                        return
                    previous = signature
                    time.sleep(0.1)
                raise RuntimeError(f"{version}: synthetic PR did not finish its asynchronous mergeability check.")

            for user in ("p0-owner", "p0-reviewer", "p0-limited"):
                run(["auth", "login", "--credential-source", "git", "--name", "team", "--confirm-instance"], user=user)
            label = api(base + "/labels", "POST", {"name": "p4-bug", "color": "ee0000"})
            milestone = api(base + "/milestones", "POST", {"title": "p4-milestone"})
            write(["pr", "create", "--title", "P4 missing head", "--base", "main", "--head", "not-pushed"], code=1)
            write(["pr", "create", "--title", "P4 identical branches", "--base", "main", "--head", "main"], code=2)
            assert not api(base + "/pulls?state=all")
            checks.append("unpublished_or_identical_head_rejected_without_creation")
            branch("feature")
            notes = directory / "notes.txt"
            notes.write_text("P4 notes\n中文内容\n")
            created = write(["pr", "create", "--title", "P4 primary", "--base", "main", "--head", "feature", "--body-file", str(notes),
                             "--assignee", "p0-owner", "--label", str(label["id"]), "--milestone", str(milestone["id"]), "--reviewer", "p0-reviewer"])
            pr = created["items"][0]["data"]["resource"]
            number = str(pr["number"])
            run(["pr", "view", number])
            listed = run(["pr", "list", "--base", "main", "--author", "p0-owner", "--search", "P4 primary", "--all"])
            assert [value["number"] for value in listed["items"][0]["data"]["items"]] == [pr["number"]]
            run(["pr", "files", number, "--all"])
            run(["pr", "commits", number, "--all"])
            write(["pr", "comment", "add", number, "--body", "P4 discussion"])
            comments = run(["pr", "comment", "list", number, "--all"])
            assert any(value["body"] == "P4 discussion" for value in comments["items"][0]["data"]["items"])
            write(["pr", "reviewer", "add", number, "--team", "Reviewers"])
            write(["pr", "review", "create", number, "--event", "comment", "--body", "P4 review comment"], user="p0-reviewer")
            write(["pr", "review", "create", number, "--event", "request-changes", "--body", "P4 requested changes"], user="p0-reviewer")
            write(["pr", "review", "create", number, "--event", "approve"], user="p0-reviewer")
            pending = api(base + "/pulls/" + number + "/reviews", "POST", {"event": "PENDING", "body": "P4 pending review", "commit_id": pr["head"]["sha"]}, user="p0-reviewer")
            write(["pr", "review", "create", number, "--event", "comment", "--body", "P4 submitted pending review", "--review-id", str(pending["id"])], user="p0-reviewer")
            checks.append("existing_pending_review_submission")
            reviews = run(["pr", "review", "list", number, "--all"])
            assert {"COMMENT", "REQUEST_CHANGES", "APPROVED"}.issubset({value["state"] for value in reviews["items"][0]["data"]["items"]})
            before_reviews = [value["id"] for value in api(base + "/pulls/" + number + "/reviews")]
            write(["pr", "review", "create", number, "--event", "comment", "--body", " "], code=2, user="p0-reviewer")
            write(["pr", "review", "create", number, "--event", "request-changes", "--body", " "], code=2, user="p0-reviewer")
            # 旧版允许只读协作者请求审查；公开仓库非协作者才是此处的无权身份。
            api(base + "/collaborators/p0-limited", "DELETE")
            for flags in (["--user", "p0-reviewer"], ["--team", "Reviewers"]):
                denied = write(["pr", "reviewer", "add", number, *flags], code=1, user="p0-limited")
                failure = denied["items"][0]["error"]
                if (failure["code"], failure.get("http_status")) not in (("PERMISSION_DENIED", 403), ("NOT_FOUND", 404), ("SERVER_ERROR", 422)):
                    raise RuntimeError(f"Unexpected reviewer permission result: {failure['code']}; HTTP {failure.get('http_status')}.")
                permission_denials.append({"operation": "pr.reviewer.add", "field": flags[0], "http_status": failure["http_status"], "code": failure["code"]})
            assert [value["id"] for value in api(base + "/pulls/" + number + "/reviews")] == before_reviews
            api(base + "/collaborators/p0-limited", "PUT", {"permission": "read"})
            checks.append("empty_review_body_and_user_team_permission_rejected_without_review_changes")
            checks.append("pr_create_fields_filters_files_commits_discussion_reviewers_and_three_review_events")
            print(version + ": PR create, discussion, and reviews passed.", flush=True)
            write(["pr", "edit", number, "--title", "P4 edited", "--clear-body", "--clear-assignees", "--clear-labels", "--clear-milestone"])
            current = api(base + "/pulls/" + number)
            assert current["body"] == "" and not current["labels"] and not current["assignees"] and current["milestone"] is None
            write(["pr", "edit", number, "--body", "restored", "--assignee", "p0-owner", "--label", str(label["id"]), "--milestone", str(milestone["id"])])
            write(["pr", "close", number, number])
            skipped = write(["pr", "close", number])
            assert skipped["items"][0]["status"] == "skipped"
            write(["pr", "reopen", number])
            ordinary = api(base + "/issues", "POST", {"title": "P4 ordinary issue"})
            rejected = write(["pr", "close", str(ordinary["number"])], code=2)
            assert rejected["items"][0]["error"]["code"] == "RESOURCE_TYPE_MISMATCH"
            partial = write(["pr", "close", number, "99999999"], code=3)
            assert [item["status"] for item in partial["items"]] == ["success", "failed"]
            write(["pr", "reopen", number])
            run(["pr", "close", number], code=2)
            write(["pr", "close"], code=2)
            checks.append("pr_explicit_clears_batch_dedup_skipped_partial_type_and_confirmation")
            api(base, "PATCH", {"has_pull_requests": True, "allow_squash_merge": False})
            assert not api(base)["allow_squash_merge"]
            write(["pr", "merge", number, "--method", "squash"], code=1)
            write(["pr", "merge", number], code=1, user="p0-limited")
            write(["pr", "merge", number, "--delete-branch"])
            assert api(base + "/pulls/" + number)["merged"]
            api(base + "/branches/feature", expected=404)
            write(["pr", "reopen", number], code=1)
            checks.append("merge_method_rejection_permission_denied_head_bound_merge_and_explicit_branch_deletion")
            print(version + ": PR edit, batch, and merge passed.", flush=True)
            # 显式 fork 的创建与清理均属于本轮随机仓库，不猜测现有用户仓库。
            api(base + "/forks", "POST", {"name": prefix + "-fork"}, user="p0-reviewer")
            fork_created = True
            fork_base = "/repos/p0-reviewer/" + prefix + "-fork"
            branch("fork-feature", fork_base, "p0-reviewer")
            cross = write(["pr", "create", "--title", "P4 explicit fork", "--base", "main", "--head", "p0-reviewer/" + prefix + "-fork:fork-feature"])
            cross_number = str(cross["items"][0]["data"]["resource"]["number"])
            write(["pr", "close", cross_number])
            checks.append("explicit_fork_head_without_automatic_push")
            api(base, "PATCH", {"has_pull_requests": True, "allow_squash_merge": True, "allow_rebase": True, "allow_rebase_explicit": True, "allow_fast_forward_only_merge": True})
            for method in ("squash", "rebase", "rebase-merge", "fast-forward-only"):
                name = "style-" + method
                method_base = "base-" + method
                api(base + "/branches", "POST", {"new_branch_name": method_base, "old_branch_name": "main"})
                branch(name, base_branch=method_base)
                candidate = write(["pr", "create", "--title", "P4 method " + method, "--base", method_base, "--head", name])
                candidate_number = str(candidate["items"][0]["data"]["resource"]["number"])
                wait_mergeable(candidate_number)
                write(["pr", "merge", candidate_number, "--method", method])
                assert api(base + "/pulls/" + candidate_number)["merged"]
                assert api(base + "/branches/" + name)["name"] == name
            checks.append("all_five_merge_methods_and_default_source_branch_retention")
            branch("protected-feature")
            protected = write(["pr", "create", "--title", "P4 protected PR", "--base", "main", "--head", "protected-feature"])
            protected_number = str(protected["items"][0]["data"]["resource"]["number"])
            wait_mergeable(protected_number)
            api(base + "/branch_protections", "POST", {"branch_name": "main", "enable_status_check": True, "status_check_contexts": ["sotea/p4-required"]})
            write(["pr", "merge", protected_number], code=1)
            assert not api(base + "/pulls/" + protected_number)["merged"]
            api(base + "/branch_protections/main", "DELETE")
            write(["pr", "close", protected_number])
            checks.append("protected_branch_missing_status_does_not_merge_or_bypass")
            branch("conflict")
            source_readme = api(base + "/contents/README.md?ref=conflict")
            api(base + "/contents/README.md", "PUT", {"branch": "conflict", "sha": source_readme["sha"], "content": base64.b64encode(b"P4 source conflict\n").decode(), "message": "P4 source conflict"})
            target_readme = api(base + "/contents/README.md?ref=main")
            api(base + "/contents/README.md", "PUT", {"branch": "main", "sha": target_readme["sha"], "content": base64.b64encode(b"P4 target conflict\n").decode(), "message": "P4 target conflict"})
            conflict = write(["pr", "create", "--title", "P4 conflicting PR", "--base", "main", "--head", "conflict"])
            conflict_number = str(conflict["items"][0]["data"]["resource"]["number"])
            deadline = time.monotonic() + 10
            while time.monotonic() < deadline:
                status, files = fixture_pull_state(conflict_number)
                if status == "0" and "README.md" in files:
                    break
                time.sleep(0.1)
            else:
                raise RuntimeError(f"{version}: the fixture did not reach a verified README conflict.")
            write(["pr", "merge", conflict_number], code=1)
            assert not api(base + "/pulls/" + conflict_number)["merged"]
            write(["pr", "close", conflict_number])
            checks.append("merge_conflict_preserved_without_force_or_fallback")
            tag = "p4/中文-" + prefix
            write(["release", "create", tag], code=2)
            draft = write(["release", "create", tag, "--target", "main", "--title", "P4 release", "--notes-file", str(notes)])
            release_value = draft["items"][0]["data"]["resource"]
            assert release_value["draft"] and not release_value["prerelease"]
            run(["release", "view", tag])
            releases = run(["release", "list", "--search", prefix, "--all"])
            assert any(value["tag_name"] == tag for value in releases["items"][0]["data"]["items"])
            write(["release", "create", tag, "--target", "main"], code=1)
            denied_tag = "denied-" + prefix
            denied = write(["release", "create", denied_tag, "--target", "main"], code=1, user="p0-limited")
            failure = denied["items"][0]["error"]
            if (failure["code"], failure.get("http_status")) not in (("PERMISSION_DENIED", 403), ("NOT_FOUND", 404), ("SERVER_ERROR", 422)):
                raise RuntimeError(f"Unexpected release permission result: {failure['code']}; HTTP {failure.get('http_status')}.")
            permission_denials.append({"operation": "release.create", "http_status": failure["http_status"], "code": failure["code"]})
            api(base + "/releases/tags/" + denied_tag, expected=404)
            write(["release", "asset", "upload", tag, str(directory)], code=2)
            write(["release", "asset", "upload", tag, str(directory / "missing.bin")], code=1)
            unreadable = directory / "unreadable.bin"
            unreadable.write_bytes(b"unreadable")
            unreadable.chmod(0)
            try:
                write(["release", "asset", "upload", tag, str(unreadable)], code=1)
            finally:
                unreadable.chmod(0o600)
            assert not api(base + f"/releases/{release_value['id']}/assets")
            checks.append("release_permission_and_nonregular_missing_unreadable_file_rejected_without_assets")
            file = directory / "中文 empty.bin"
            file.write_bytes(b"")
            write(["release", "asset", "upload", tag, str(file), "--name", "empty.bin"])
            source = directory / "payload.bin"
            source.write_bytes(bytes(range(256)) * 4096)
            uploaded = write(["release", "asset", "upload", tag, str(source), "--name", "bundle.bin"])
            assert uploaded["items"][0]["data"]["resource"]["asset"]["size"] == source.stat().st_size
            write(["release", "asset", "upload", tag, str(source), "--name", "bundle.bin"], code=1)
            assets = run(["release", "asset", "list", tag, "--all"])
            assert {value["name"] for value in assets["items"][0]["data"]["items"]} == {"empty.bin", "bundle.bin"}
            assert api(base + f"/releases/{release_value['id']}")["draft"]
            write(["release", "edit", tag, "--title", "P4 edited release", "--prerelease"])
            value = api(base + f"/releases/{release_value['id']}")
            assert value["draft"] and value["prerelease"] and value["body"] == notes.read_text()
            write(["release", "edit", tag, "--publish", "--draft"], code=2)
            write(["release", "edit", tag, "--publish", "--prerelease=false"])
            value = api(base + f"/releases/{release_value['id']}")
            assert not value["draft"] and not value["prerelease"]
            assert api(base + "/tags/" + urllib.parse.quote(tag, safe=""))["commit"]["sha"]
            checks.append("release_new_tag_default_draft_notes_boolean_presence_empty_and_binary_assets_conflict_and_explicit_publish")
            existing = "existing-" + prefix
            api(base + "/tags", "POST", {"tag_name": existing, "target": "main"})
            write(["release", "create", existing, "--publish"])
            checks.append("release_existing_tag_without_implicit_target_or_replacement")
            print(version + ": Release draft, assets, and publishing passed.", flush=True)
    finally:
        primary_failure = sys.exc_info()[1]
        failures = []
        for enabled, path in ((fork_created, "/repos/p0-reviewer/" + prefix + "-fork"), (repository_created, base), (organization_created, "/orgs/" + organization)):
            if enabled:
                try:
                    api(path, "DELETE", user="p0-reviewer" if path.startswith("/repos/p0-reviewer/") else "p0-owner")
                except RuntimeError as error:
                    failures.append(str(error))
        if failures:
            raise RuntimeError("P4 scoped cleanup failed; primary=" + str(primary_failure) + "; " + "; ".join(failures)) from None
    return {"version": version, "status": "passed", "checks": checks, "permission_denials": permission_denials, "cleanup": "passed"}


def main():
    """运行明确版本清单并保留失败状态和已通过结果，不以相邻版替代。

    记录只包含产物摘要、场景与状态；真实密钥和完整访问日志均不归档。
    """
    parser = argparse.ArgumentParser(description="Verify dedicated PR and Release workflows in isolated Gitea instances.")
    parser.add_argument("--binary", type=Path, default=ROOT / "dist/p4/darwin-arm64/sotea")
    parser.add_argument("--versions", nargs="+", choices=fixture.VERSIONS, default=list(fixture.VERSIONS))
    args = parser.parse_args()
    binary = args.binary.resolve()
    record = {"schema_version": 1, "time_utc": datetime.now(timezone.utc).isoformat(), "binary_sha256": hashlib.sha256(binary.read_bytes()).hexdigest(), "versions": []}
    try:
        for version in args.versions:
            try:
                record["versions"].append(smoke(binary, version))
            except Exception as error:
                record["versions"].append({"version": version, "status": "failed", "failure_type": type(error).__name__, "failure_summary": str(error) if isinstance(error, RuntimeError) else "See the failing assertion location in the command output"})
                raise
    finally:
        (fixture.STATE / "p4-workflow-result.json").write_text(json.dumps(record, indent=2) + "\n")


if __name__ == "__main__":
    try:
        main()
    except (RuntimeError, AssertionError, OSError, subprocess.TimeoutExpired, subprocess.CalledProcessError) as error:
        if isinstance(error, RuntimeError):
            sys.exit(str(error))
        frame = traceback.extract_tb(error.__traceback__)[-1]
        sys.exit(f"P4 isolated validation failed at {Path(frame.filename).name}:{frame.lineno} ({type(error).__name__}).")
