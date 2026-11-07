#!/usr/bin/env python3
"""在七版隔离 Gitea 上验证最终产物的真实持续菜单链路。

仅创建随机 p5 前缀资源及独立 token，清理按精确 ID，不修改原有资源或权限。
"""
import argparse
import base64
from datetime import datetime, timezone
import hashlib
import importlib.util
import json
from pathlib import Path
import secrets
import subprocess
import sys
import tempfile
import urllib.error
import urllib.request

ROOT = Path(__file__).resolve().parents[1]


def module(name, filename):
    """加载现有最小夹具和 PTY 驱动，保持同一终端验证语义。

    不调用其 main，也不打印开发者或合成认证材料。
    """
    spec = importlib.util.spec_from_file_location(name, ROOT / "scripts" / filename)
    loaded = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(loaded)
    return loaded


fixture = module("p2_fixture", "p2-readonly-smoke.py")
terminal_module = module("p5_terminal", "p5-terminal-smoke.py")


def smoke(binary, version):
    """从仓库子目录无参数启动，完成登录、字段选择、关闭、草稿和上传。

    每个确认前核对远端实际状态；最终清理全部精确资源及 token。
    """
    root_url = f"http://127.0.0.1:{fixture.VERSIONS[version]}"
    credentials = json.loads((fixture.STATE / f"credentials-{version}.json").read_text())
    prefix = "sotea-p5-" + secrets.token_hex(8)
    base = "/repos/" + fixture.REPOSITORY
    personal_name = prefix + "-personal"
    organization_name = prefix + "-org"
    organization_repo_name = prefix + "-org-repo"
    personal_path = "/repos/p0-owner/" + personal_name
    organization_path = "/orgs/" + organization_name
    organization_repo_path = "/repos/" + organization_name + "/" + organization_repo_name
    ids = {}
    terminal = None

    setup_user = "p4-bootstrap"
    setup_path = fixture.STATE / f"p4-bootstrap-{version}.json"
    if not setup_path.exists():
        raise RuntimeError(f"{version}: run the existing P4 setup before creating the isolated organization fixture.")
    credentials[setup_user] = json.loads(setup_path.read_text())["password"]

    def api(path, method="GET", payload=None, user="p0-owner"):
        """固定到当前版本及合成 owner 的初始化／清理 API。

        失败只显示方法路径和状态，不包含请求头或响应正文。
        """
        authorization = base64.b64encode((user + ":" + credentials[user]).encode()).decode()
        request = urllib.request.Request(
            root_url + "/api/v1" + path,
            data=None if payload is None else json.dumps(payload).encode(),
            method=method,
            headers={"Authorization": "Basic " + authorization, "Content-Type": "application/json"},
        )
        opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), fixture.NoRedirect())
        try:
            with opener.open(request, timeout=30) as response:
                raw = response.read(8 << 20)
        except urllib.error.HTTPError as error:
            raise RuntimeError(f"{version}: fixture {method} {path} returned HTTP {error.code}.") from None
        except (urllib.error.URLError, TimeoutError, OSError):
            raise RuntimeError(f"{version}: fixture {method} {path} could not reach the isolated instance.") from None
        return json.loads(raw) if raw else None

    def delete_by_id(path, expected_id, user):
        """在删除随机浏览资源前核对真实 Gitea ID。

        API 通过 owner/name 定位删除目标，只有当前路径仍映射预期 ID 才执行删除。
        """
        current = api(path, user=user)
        if current.get("id") != expected_id:
            raise RuntimeError(f"{version}: cleanup refused because {path} no longer has its created ID.")
        api(path, "DELETE", user=user)

    try:
        token = api("/users/p0-owner/tokens", "POST", {"name": prefix, "scopes": ["all"]})
        ids["token"] = token["id"]
        personal = api("/user/repos", "POST", {
            "name": personal_name,
            "auto_init": True,
            "default_branch": "main",
        })
        ids["personal_repository"] = personal["id"]
        organization = api("/orgs", "POST", {
            "username": organization_name,
            "full_name": prefix,
            "visibility": "public",
        }, user=setup_user)
        ids["organization"] = organization["id"]
        owners = next(team for team in api(organization_path + "/teams", user=setup_user)
                      if team["name"] == "Owners")
        api(f"/teams/{owners['id']}/members/p0-owner", "PUT", user=setup_user)
        organization_repository = api(organization_path + "/repos", "POST", {
            "name": organization_repo_name,
            "auto_init": True,
            "default_branch": "main",
        }, user=setup_user)
        ids["organization_repository"] = organization_repository["id"]
        visible_organizations = api("/user/orgs")
        if not any(value["id"] == ids["organization"] for value in visible_organizations):
            raise RuntimeError(f"{version}: the account cannot see the organization it was added to.")
        label = api(base + "/labels", "POST", {"name": prefix, "color": "#336699"})
        ids["label"] = label["id"]
        issue = api(base + "/issues", "POST", {"title": prefix + " 中文👩🏽‍💻", "body": "original"})
        ids["issue"] = issue["number"]
        with tempfile.TemporaryDirectory(prefix="sotea-p5-real-") as temporary:
            directory = Path(temporary)
            worktree = directory / "中文 repository"
            subprocess.run(["git", "init", "--quiet", str(worktree)], check=True)
            subprocess.run(["git", "-C", str(worktree), "remote", "add", "team",
                            root_url + "/" + fixture.REPOSITORY + ".git"], check=True)
            subdirectory = worktree / "subdirectory"
            subdirectory.mkdir()
            config = directory / "config.json"
            config.write_text(json.dumps({"version": 1, "revision": 1, "instances": {"isolated": {
                "url": root_url, "proxy": "direct", "active_account": "p0-owner", "accounts": {
                    "p0-owner": {"source": "environment", "instance_url": root_url}}}}, "repositories": []}))
            notes = directory / "中文 notes.md"
            notes.write_text("P5 menu release notes.\n")
            attachment = directory / "中文 attachment.bin"
            attachment.write_bytes(bytes(range(256)))
            terminal = terminal_module.Terminal(binary, subdirectory, config, repository=None, instance=None,
                                               environment_extra={"SOTEA_TOKEN": token["sha1"]})
            terminal.wait(fixture.REPOSITORY)
            terminal.wait("Home ·")
            terminal.wait("@p0-owner")
            terminal.action("repo", "view")
            terminal.group("Overview")
            terminal.wait(fixture.REPOSITORY)
            terminal.home()

            before_browse = config.read_bytes()
            terminal.choose("Home ·", "Browse account")
            terminal.choose("Browse account ·", "repositories")
            terminal.choose("Account repositories · Page 1", personal["full_name"])
            terminal.wait("Home ·")
            terminal.wait(personal["full_name"])
            terminal.action("repo", "view")
            terminal.group("Overview")
            terminal.wait(personal["full_name"])
            terminal.home()
            terminal.choose("Home ·", "Back to repository list")
            terminal.wait("Account repositories · Page 1")
            terminal.wait(personal_name)
            terminal.choose("Account repositories · Page 1", "Back")
            terminal.choose("Browse account ·", "organizations")
            terminal.wait("Organizations · Page 1")
            terminal.choose("Organizations · Page 1", organization_name)
            organization_title = "Organization repositories · " + organization_name
            terminal.wait(organization_title)
            organization_full_name = organization_name + "/" + organization_repo_name
            terminal.choose(organization_title, organization_full_name)
            terminal.wait("Home ·")
            terminal.wait(organization_full_name)
            terminal.action("repo", "view")
            terminal.group("Overview")
            terminal.wait(organization_full_name)
            terminal.home()
            terminal.choose("Home ·", "Back to repository list")
            terminal.wait(organization_title)
            terminal.choose(organization_title, "Back")
            terminal.wait("Organizations · Page 1")
            terminal.choose("Organizations · Page 1", "Back")
            terminal.wait("Browse account ·")
            terminal.choose("Browse account ·", "Back to Home")
            terminal.wait("Home ·")
            terminal.wait(fixture.REPOSITORY)
            if config.read_bytes() != before_browse:
                raise RuntimeError("Account browsing changed persistent configuration.")

            terminal.home()
            terminal.choose("Home ·", "auth")
            terminal.choose("Account", "auth login")
            terminal.wait("Step 1 of 4")
            terminal.send(b"\r")
            terminal.wait("Step 2 of 4")
            terminal.send(b"\r")
            terminal.choose("Step 3 of 4", "environment")
            terminal.wait("Step 4 of 4")
            terminal.choose("Step 4 of 4", "save")
            terminal.wait("Account connected")
            terminal.choose("Account connected", "continue")
            if token["sha1"] in config.read_text():
                raise RuntimeError("The environment token was persisted to ordinary configuration.")

            terminal.action("issue", "edit", options=True)
            terminal.choose("Fields ·", "--label ·")
            terminal.choose("--label", "Choose repository candidates")
            terminal.choose("Repository candidates", prefix)
            terminal.fields_done("issue edit")
            terminal.choose("Select issues", prefix)
            terminal.wait("[y/N]")
            if api(base + f"/issues/{ids['issue']}")["labels"]:
                raise RuntimeError("The label was applied before confirmation.")
            terminal.send(b"y\r")
            terminal.wait("issue edit · exit 0")
            if [item["id"] for item in api(base + f"/issues/{ids['issue']}")["labels"]] != [ids["label"]]:
                raise RuntimeError("The selected stable label ID was not applied.")

            terminal.action("issue", "close")
            terminal.choose("Select issues", prefix)
            terminal.wait("[y/N]")
            terminal.send(b"\r")
            terminal.wait("issue close · exit 130")
            if api(base + f"/issues/{ids['issue']}")["state"] != "open":
                raise RuntimeError("Default No mutated the actual server.")
            terminal.action("issue", "close")
            terminal.choose("Select issues", prefix)
            terminal.wait("[y/N]")
            terminal.send(b"y\r")
            terminal.wait("issue close · exit 0")

            terminal.action("release", "create")
            terminal.choose("Fields ·", "--target ·")
            terminal.choose("--target", "Choose repository candidates")
            terminal.choose("Repository candidates", "main · main")
            terminal.choose("Fields ·", "--notes-file ·")
            terminal.choose("--notes-file", "Enter a value")
            terminal.wait("Value for --notes-file")
            terminal.send((str(notes) + "\r").encode())
            terminal.fields_done("release create")
            terminal.choose("Release tag", "Enter a tag explicitly")
            terminal.wait("Release tag (")
            terminal.send((prefix + "\r").encode())
            terminal.wait("[y/N]")
            terminal.send(b"y\r")
            terminal.wait("release create · exit 0")
            release = api(base + "/releases/tags/" + prefix)
            ids["release"] = release["id"]
            if not release["draft"] or release["body"] != notes.read_text():
                raise RuntimeError("Release notes or draft state was lost.")

            terminal.action("release", "asset upload")
            terminal.fields_done("release asset upload")
            terminal.choose("Select release", prefix)
            terminal.wait("Single attachment file path")
            terminal.send((str(attachment) + "\r").encode())
            terminal.wait("[y/N]")
            terminal.send(b"y\r")
            terminal.wait("release asset upload · exit 0")
            assets = api(base + f"/releases/{ids['release']}/assets")
            if len(assets) != 1 or assets[0]["size"] != 256:
                raise RuntimeError("The single file upload could not be verified.")
            terminal.home()
            terminal.choose("Home ·", "Quit")
            terminal.close()
            if token["sha1"].encode() in terminal.all_output:
                raise RuntimeError("A real fixture token reached terminal output.")
            terminal = None
        return {"version": version, "status": "passed", "cleanup": "passed",
                "checks": ["subdirectory_context_discovery", "home_api_user_profile", "readable_repository_overview",
                           "account_repository_browse_workbench", "organization_repository_browse_workbench",
                           "stable_id_resource_entry", "browse_original_context_restored", "menu_environment_login", "stable_label_candidates",
                           "default_no_zero_write", "issue_close", "branch_target_candidates",
                           "unicode_notes_file", "release_draft", "unicode_single_attachment"]}
    finally:
        primary_failure = sys.exc_info()[1]
        cleanup_failures = []
        if terminal is not None:
            terminal.abort()
        # 先通过精确随机标签发现创建后可能未记录的 Release，避免异常路径遗留资源。
        if "issue" in ids:
            try:
                releases = api(base + "/releases?limit=100")
                for release in releases:
                    if release["tag_name"] == prefix:
                        api(base + f"/releases/{release['id']}", "DELETE")
                tags = api(base + "/tags?limit=100")
                if any(tag["name"] == prefix for tag in tags):
                    api(base + "/tags/" + prefix, "DELETE")
                api(base + f"/issues/{ids['issue']}", "DELETE")
            except RuntimeError as error:
                cleanup_failures.append(str(error))
        if "label" in ids:
            try:
                api(base + f"/labels/{ids['label']}", "DELETE")
            except RuntimeError as error:
                cleanup_failures.append(str(error))
        for path, key, user in (
            (organization_repo_path, "organization_repository", setup_user),
            (personal_path, "personal_repository", "p0-owner"),
            (organization_path, "organization", setup_user),
        ):
            if key not in ids:
                continue
            try:
                delete_by_id(path, ids[key], user)
            except RuntimeError as error:
                cleanup_failures.append(str(error))
        if "token" in ids:
            try:
                api(f"/users/p0-owner/tokens/{ids['token']}", "DELETE")
            except RuntimeError as error:
                cleanup_failures.append(str(error))
        if cleanup_failures:
            primary = type(primary_failure).__name__ if primary_failure is not None else "none"
            raise RuntimeError("P5 scoped cleanup failed; primary=" + primary + "; " + "; ".join(cleanup_failures))


def main():
    """按指定版本运行真实菜单场景，允许分批轮换固定 Docker 镜像。

    append 仅合并同一二进制摘要的结果，不能将旧产物验证冒充最终产物。
    """
    parser = argparse.ArgumentParser()
    parser.add_argument("--binary", type=Path, default=ROOT / "dist/p5/darwin-arm64/sotea")
    parser.add_argument("--versions", nargs="+", choices=fixture.VERSIONS, default=list(fixture.VERSIONS))
    parser.add_argument("--append", action="store_true")
    parser.add_argument("--result", type=Path, default=fixture.STATE / "p5-gitea-menu-result.json")
    args = parser.parse_args()
    binary = args.binary.resolve()
    digest = hashlib.sha256(binary.read_bytes()).hexdigest()
    path = args.result
    record = {"schema_version": 1, "binary_sha256": digest, "versions": []}
    if args.append and path.exists():
        record = json.loads(path.read_text())
        if record["binary_sha256"] != digest:
            raise RuntimeError("Cannot append evidence from a different binary.")
    for version in args.versions:
        result = smoke(binary, version)
        record["versions"] = [row for row in record["versions"] if row["version"] != version] + [result]
        record["time_utc"] = datetime.now(timezone.utc).isoformat()
        path.write_text(json.dumps(record, indent=2) + "\n")
        print(f"P5 {version} real menu workflow and scoped cleanup passed.", flush=True)


if __name__ == "__main__":
    main()
