#!/usr/bin/env python3
"""准备和演练固定的 sotea P0 隔离环境。

仅管理 sotea-p0 Compose 项目及固定本机端口；不接受远端实例地址。
"""

import argparse
import base64
from datetime import datetime, timezone
import json
from pathlib import Path
import secrets
import subprocess
import time
import urllib.error
import urllib.request

ROOT = Path(__file__).resolve().parents[1]
STATE = ROOT / "tests/integration/.state"
COMPOSE = ROOT / "tests/integration/compose.yaml"
VERSIONS = {"1.22.6": 32206, "1.23.8": 32308, "1.24.7": 32407, "1.25.5": 32505,
            "1.26.4": 32604, "1.27.3": 32703, "28.3.0": 32830}
USERS = ("p0-owner", "p0-reviewer", "p0-limited")
REPO = "sotea-p0"


def run(args, timeout=120, env=None):
    """执行固定参数的子进程并捕获输出。

    失败不打印参数或完整输出，避免泄露潜在敏感信息。
    """
    result = subprocess.run(args, cwd=ROOT, text=True, capture_output=True, timeout=timeout, env=env)
    if result.returncode:
        raise RuntimeError(f"{Path(args[0]).name} 执行失败，退出码 {result.returncode}")
    return result.stdout.strip()


def compose(*args):
    """只操作固定的隔离 Compose 文件与项目。

    调用者不能通过参数替换配置文件或项目名。
    """
    return run(["docker", "compose", "-p", "sotea-p0", "-f", str(COMPOSE), *args], timeout=600)


def write_json(path, data):
    """将本地运行数据写入受忽略的状态目录。

    含合成凭证的文件仅允许当前用户读取。
    """
    STATE.mkdir(parents=True, exist_ok=True, mode=0o700)
    path.write_text(json.dumps(data, ensure_ascii=False, indent=2) + "\n")
    path.chmod(0o600)


class NoRedirect(urllib.request.HTTPRedirectHandler):
    """阻止测试 API 的认证请求跨地址重定向。

    即使本机端口意外返回跳转，也不会向其他来源发送测试凭证。
    """

    def redirect_request(self, request, file, code, message, headers, new_url):
        """将任何重定向交回 HTTP 错误处理。

        此返回值使 urllib 不再创建目标地址请求。
        """
        return None


def api(version, method, path, user=None, body=None, expected=(200, 201, 204)):
    """向清单中固定本机端口发送有界 API 请求。

    返回解析后的 JSON 或原始字节，错误消息仅包含方法、路径和状态。
    """
    url = f"http://127.0.0.1:{VERSIONS[version]}/api/v1{path}"
    headers = {"Content-Type": "application/json"}
    if user:
        credentials = json.loads((STATE / f"credentials-{version}.json").read_text())
        auth = base64.b64encode(f"{user}:{credentials[user]}".encode()).decode()
        headers["Authorization"] = "Basic " + auth
    data = None if body is None else json.dumps(body).encode()
    request = urllib.request.Request(url, data=data, headers=headers, method=method)
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), NoRedirect())
    try:
        with opener.open(request, timeout=20) as response:
            status, raw = response.status, response.read(2 * 1024 * 1024)
    except urllib.error.HTTPError as error:
        status, raw = error.code, error.read(4096)
    if status not in expected:
        raise RuntimeError(f"{method} {path}: HTTP {status}，预期 {expected}")
    if not raw:
        return None
    try:
        return json.loads(raw)
    except (json.JSONDecodeError, UnicodeDecodeError):
        return raw


def start(version):
    """启动一个精确版本并验证其声明的远端版本。

    就绪等待有截止时间，其他版本不能替代指定版本。
    """
    service = "gitea-" + version.replace(".", "-")
    compose("up", "-d", service)
    deadline = time.monotonic() + 120
    while time.monotonic() < deadline:
        try:
            actual = api(version, "GET", "/version")["version"]
            if actual != version:
                raise RuntimeError(f"服务声明版本 {actual} 与 {version} 不同")
            return service
        except (OSError, urllib.error.URLError):
            time.sleep(1)
    raise RuntimeError(f"{version} 在 120 秒内未就绪")


def accounts(version, service):
    """为独立实例创建三个固定角色的合成账号。

    密码随机生成并存于被忽略文件，不进入进度输出或报告。
    """
    path = STATE / f"credentials-{version}.json"
    if path.exists():
        credentials = json.loads(path.read_text())
    else:
        credentials = {user: secrets.token_urlsafe(24) for user in USERS}
        write_json(path, credentials)
    container = compose("ps", "-q", service)
    for user in USERS:
        identity = api(version, "GET", "/user", user, expected=(200, 401))
        if identity.get("login") == user:
            continue
        run(["docker", "exec", "--user", "git", container, "/usr/local/bin/gitea",
             "--config", "/data/gitea/conf/app.ini", "admin", "user", "create",
             "--username", user, "--password", credentials[user],
             "--email", user + "@sotea.invalid", "--must-change-password=false"])
        if api(version, "GET", "/user", user)["login"] != user:
            raise RuntimeError("测试账号创建后身份验证失败")


def delete_repositories(version):
    """仅删除预先命名的隔离测试仓库。

    账号、其他仓库和 Compose 项目外的数据不会被删除。
    """
    for user in ("p0-reviewer", "p0-owner"):
        api(version, "DELETE", f"/repos/{user}/{REPO}", user, expected=(204, 404))


def update_readme(version, branch, text):
    """更新指定测试分支的 README 以制造可控冲突。

    基础 SHA 从该分支实际读取，修改仅作用于固定测试仓库。
    """
    path = f"/repos/p0-owner/{REPO}/contents/README.md"
    current = api(version, "GET", path + "?ref=" + branch, "p0-owner")
    api(version, "PUT", path, "p0-owner", {"branch": branch, "sha": current["sha"],
        "content": base64.b64encode(text.encode()).decode(), "message": "P0 conflict fixture"})


def upload_asset(version, release_id):
    """上传最小合成附件以准备后续附件读取场景。

    只向固定本机实例发送，原始请求和凭证均不写入报告。
    """
    boundary = secrets.token_hex(16)
    body = (f"--{boundary}\r\nContent-Disposition: form-data; name=\"attachment\"; "
            f"filename=\"p0.txt\"\r\nContent-Type: text/plain\r\n\r\nP0 attachment\n\r\n--{boundary}--\r\n").encode()
    credentials = json.loads((STATE / f"credentials-{version}.json").read_text())
    auth = base64.b64encode(f"p0-owner:{credentials['p0-owner']}".encode()).decode()
    request = urllib.request.Request(
        f"http://127.0.0.1:{VERSIONS[version]}/api/v1/repos/p0-owner/{REPO}/releases/{release_id}/assets",
        data=body, method="POST", headers={"Authorization": "Basic " + auth,
        "Content-Type": "multipart/form-data; boundary=" + boundary})
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), NoRedirect())
    with opener.open(request, timeout=20) as response:
        if response.status != 201:
            raise RuntimeError("附件初始化失败")


def seed(version):
    """创建首版业务需要的最小资源集合并返回编号。

    仅操作固定测试账号和仓库，重复演练先由 reset 清理。
    """
    repo = api(version, "POST", "/user/repos", "p0-owner",
               {"name": REPO, "private": True, "auto_init": True, "default_branch": "main"})
    root = f"/repos/p0-owner/{REPO}"
    api(version, "PUT", root + "/collaborators/p0-reviewer", "p0-owner", {"permission": "write"})
    label = api(version, "POST", root + "/labels", "p0-owner", {"name": "p0-bug", "color": "d73a4a"})
    milestone = api(version, "POST", root + "/milestones", "p0-owner", {"title": "p0-milestone"})
    issues = []
    for number in range(3):
        issue = api(version, "POST", root + "/issues", "p0-owner", {"title": f"P0 Issue {number + 1}",
            "body": "仅用于 sotea 隔离验证", "labels": [label["id"]],
            "milestone": milestone["id"], "assignees": ["p0-reviewer"]})
        issues.append(issue["number"])
    api(version, "PATCH", root + f"/issues/{issues[2]}", "p0-owner", {"state": "closed"})
    api(version, "POST", root + f"/issues/{issues[0]}/comments", "p0-owner", {"body": "P0 ordinary comment"})
    for branch in ("p0-feature", "p0-conflict"):
        api(version, "POST", root + "/branches", "p0-owner", {"new_branch_name": branch, "old_branch_name": "main"})
    api(version, "POST", root + "/contents/p0.txt", "p0-owner", {"branch": "p0-feature",
        "content": base64.b64encode(b"P0 clean feature\n").decode(), "message": "P0 clean feature"})
    update_readme(version, "p0-conflict", "P0 conflicting source\n")
    update_readme(version, "main", "P0 conflicting target\n")
    pulls = []
    for branch in ("p0-feature", "p0-conflict"):
        pull = api(version, "POST", root + "/pulls", "p0-owner", {"head": branch, "base": "main", "title": branch})
        pulls.append(pull["number"])
    api(version, "POST", root + f"/pulls/{pulls[0]}/requested_reviewers", "p0-owner", {"reviewers": ["p0-reviewer"]})
    api(version, "POST", root + "/branch_protections", "p0-owner", {"branch_name": "main",
        "enable_status_check": True, "status_check_contexts": ["sotea/p0-required"]})
    release = api(version, "POST", root + "/releases", "p0-owner", {"tag_name": "p0-v0.0.1",
        "target_commitish": "main", "name": "P0 draft", "body": "P0 release notes", "draft": True})
    upload_asset(version, release["id"])
    return {"repo": repo["full_name"], "issues": issues, "pulls": pulls, "label_id": label["id"],
            "milestone_id": milestone["id"], "release_id": release["id"], "release_tag": "p0-v0.0.1"}


def verify(version, fixture):
    """读取初始化资源并检查权限及最小分页场景。

    这是环境演练，不代表尚未实现的 sotea 业务命令已经通过兼容验收。
    """
    root = f"/repos/p0-owner/{REPO}"
    repository = api(version, "GET", root, "p0-owner")
    if repository["clone_url"] != f"http://127.0.0.1:{VERSIONS[version]}/p0-owner/{REPO}.git":
        raise RuntimeError("测试仓库 clone URL 未指向固定本机端口")
    api(version, "GET", root, "p0-limited", expected=(403, 404))
    first = api(version, "GET", root + "/issues?type=issues&state=all&limit=2&page=1", "p0-owner")
    second = api(version, "GET", root + "/issues?type=issues&state=all&limit=2&page=2", "p0-owner")
    if len(first) != 2 or len(second) != 1 or any(item.get("pull_request") for item in first + second):
        raise RuntimeError("Issue 分页或类型夹具不符合预期")
    for index in fixture["pulls"]:
        api(version, "GET", root + f"/pulls/{index}", "p0-owner")
    release = api(version, "GET", root + "/releases/tags/p0-v0.0.1", "p0-owner")
    assets = api(version, "GET", root + f"/releases/{release['id']}/assets", "p0-owner")
    if not release["draft"] or len(assets) != 1:
        raise RuntimeError("草稿或附件夹具不符合预期")
    return {"accounts": 3, "issues": 3, "pulls": 2, "draft_releases": 1, "assets": 1,
            "restricted_account_denied": True, "issue_pagination": [2, 1]}


def prepare(version, rounds):
    """在单个精确版本执行初始化、重置和只读核对。

    每轮结果独立记录，失败保留错误但不输出凭证。
    """
    result = {"version": version, "image": f"docker.gitea.com/gitea:{version}", "status": "failed", "rounds": []}
    try:
        print(f"准备 Gitea {version}", flush=True)
        service = start(version)
        container = compose("ps", "-q", service)
        image_id = run(["docker", "inspect", "--format", "{{.Image}}", container])
        result["image"] = run(["docker", "inspect", "--format", "{{.Config.Image}}", container])
        image = json.loads(run(["docker", "image", "inspect", image_id]))[0]
        result.update(image_id=image["Id"], digests=image.get("RepoDigests", []), architecture=image["Architecture"])
        result["reported_version"] = api(version, "GET", "/version")["version"]
        accounts(version, service)
        for number in range(1, rounds + 1):
            delete_repositories(version)
            fixture = seed(version)
            checks = verify(version, fixture)
            result["rounds"].append({"round": number, "checks": checks, "resources": fixture})
            print(f"Gitea {version} 第 {number} 轮初始化与核对通过", flush=True)
        result["status"] = "passed"
    except Exception as error:
        result["error"] = str(error) if isinstance(error, RuntimeError) else type(error).__name__
        print(f"Gitea {version} 未通过：{result['error']}", flush=True)
    result["time_utc"] = datetime.now(timezone.utc).isoformat()
    write_json(STATE / f"result-{version}.json", result)
    return result


def main():
    """提供环境演练和限定范围的生命周期命令。

    prepare 支持指定版本，清理只作用于固定 Compose 项目。
    """
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("command", choices=("prepare", "verify", "stop", "down", "reset"))
    parser.add_argument("--versions", nargs="+", choices=VERSIONS, default=list(VERSIONS))
    parser.add_argument("--rounds", type=int, choices=(1, 2), default=2)
    args = parser.parse_args()
    if args.command == "stop":
        compose("stop")
        return
    if args.command == "down":
        compose("down", "--volumes")
        for version in VERSIONS:
            path = STATE / f"credentials-{version}.json"
            path.unlink(missing_ok=True)
        return
    if args.command == "reset":
        for version in args.versions:
            start(version)
            delete_repositories(version)
        return
    if args.command == "verify":
        for version in args.versions:
            result = json.loads((STATE / f"result-{version}.json").read_text())
            if api(version, "GET", "/version")["version"] != version:
                raise RuntimeError("运行中的版本与期望不同")
            verify(version, result["rounds"][-1]["resources"])
            print(f"Gitea {version} 现有环境只读核对通过", flush=True)
        return
    results = [prepare(version, args.rounds) for version in args.versions]
    if any(result["status"] != "passed" for result in results):
        raise SystemExit(1)


if __name__ == "__main__":
    main()
