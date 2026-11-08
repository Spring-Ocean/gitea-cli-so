#!/usr/bin/env python3
"""构建和验证 GitHub Release 的固定三平台附件。

复用既有工具链构建与许可收集，不读取 devdocs 或真实凭证；不在本机发布。
"""

import argparse
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import tarfile
import tempfile
import zipfile

ROOT = Path(__file__).resolve().parents[1]
TARGETS = ("darwin-arm64", "darwin-amd64", "windows-amd64")
NUMBER = r"(?:0|[1-9][0-9]*)"
IDENTIFIER = rf"(?:{NUMBER}|[0-9]*[A-Za-z-][0-9A-Za-z-]*)"
TAG = re.compile(rf"v{NUMBER}\.{NUMBER}\.{NUMBER}(?:-{IDENTIFIER}(?:\.{IDENTIFIER})*)?")


def validate_tag(tag):
    """校验可公开分发的语义版本和统一 VERSION 来源。

    标签不接受构建元数据或 URL 字符；RC 仅由标准预发布后缀决定。
    """
    if len(tag) > 128 or not TAG.fullmatch(tag):
        raise ValueError("Expected a release tag such as v0.1.0 or v0.1.0-rc.1.")
    if (ROOT / "cmd/sotea/VERSION").read_text().strip() != tag:
        raise ValueError("The release tag must exactly match cmd/sotea/VERSION.")
    commit = subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=ROOT, text=True).strip()
    return {"tag": tag, "prerelease": "-" in tag, "commit": commit}


def packaging_helpers():
    """复用 P7 已有的许可收集和确定性归档函数。

    仅加载纯函数，不执行内部候选主流程，也不依赖被忽略的开发文档。
    """
    spec = importlib.util.spec_from_file_location("sotea_packaging", ROOT / "scripts/p7-package.py")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def package_release(tag, output):
    """构建三个目标并附带许可、公开文档和完整构建来源。

    输出必须是尚不存在的新目录；任何阶段失败均不生成可发布的完整清单。
    """
    metadata = validate_tag(tag)
    if output.exists():
        raise ValueError("Use a new release output directory; existing assets are never overwritten.")
    source_changes = subprocess.check_output(
        ["git", "status", "--porcelain", "--untracked-files=no"], cwd=ROOT, text=True,
    ).strip()
    if source_changes:
        raise ValueError("Release builds require a clean tracked worktree.")
    environment = dict(os.environ, GOTOOLCHAIN="go1.27.3")
    build_directory = output.parent / (output.name + "-build")
    subprocess.run(
        [sys.executable, "scripts/check.py", "--build-only", "--phase", "p7", "--output", str(build_directory)],
        cwd=ROOT, env=environment, check=True,
    )
    build = json.loads((build_directory / "build-info.json").read_text())
    if build["version"] != tag or build["worktree_dirty"]:
        raise ValueError("The build version or source state changed during packaging.")
    assemble_assets(metadata, build, output)


def assemble_assets(metadata, build, output):
    """将已构建程序与公开材料装成确定性附件并保留来源状态。

    此函数不授予发布资格；对外入口负责干净源码检查，验证入口拒绝 dirty 清单。
    """
    tag = metadata["tag"]
    helpers = packaging_helpers()
    modules, licenses = helpers.dependencies(build["artifacts"])
    epoch = int(subprocess.check_output(["git", "show", "-s", "--format=%ct", "HEAD"], cwd=ROOT, text=True))
    notices = {
        "schema_version": 1, "modules": modules,
        "go_runtime": "Go 1.27.3; original license and bundled vendor notices in LICENSES/go1.27.3",
        "localereader_note": "Original declaration and supplementary upstream license retained with provenance.",
    }
    common = {
        "LICENSE": (ROOT / "LICENSE").read_bytes(),
        "README.md": (ROOT / "README.md").read_bytes(),
        "THIRD-PARTY-NOTICES.json": (json.dumps(notices, indent=2) + "\n").encode(),
        **{"docs/" + str(path.relative_to(ROOT / "docs")): path.read_bytes()
           for path in sorted((ROOT / "docs").rglob("*")) if path.is_file()},
        **licenses,
    }
    output.mkdir(parents=True)
    for artifact in build["artifacts"]:
        target = artifact["os"] + "-" + artifact["arch"]
        folder = "sotea-" + tag + "-" + target
        name = "sotea.exe" if artifact["os"] == "windows" else "sotea"
        binary = (ROOT / artifact["path"]).read_bytes()
        if helpers.digest(binary) != artifact["sha256"]:
            raise ValueError("The binary changed after its build record was written.")
        manifest = {
            "schema_version": 1, **metadata, "version": tag, "go": build["go"],
            "os": artifact["os"], "arch": artifact["arch"],
            "build_options": build["build_options"], "binary_sha256": artifact["sha256"],
            "source_sha256": build["source_sha256"],
            "signature": "No Developer ID, notarization or Authenticode; Go may apply an ad-hoc Mach-O signature.",
        }
        files = {**common, name: binary, "BUILD-INFO.json": (json.dumps(manifest, indent=2) + "\n").encode()}
        if artifact["os"] == "windows":
            files["windows-p7-smoke.ps1"] = (ROOT / "scripts/windows-p7-smoke.ps1").read_text(encoding="utf-8-sig").encode("utf-8-sig")
        files["CONTENTS.sha256"] = "".join(helpers.digest(data) + "  " + path + "\n" for path, data in sorted(files.items())).encode()
        suffix = ".zip" if artifact["os"] == "windows" else ".tar.gz"
        (output / (folder + suffix)).write_bytes(helpers.archive_bytes(files, folder, artifact["os"], epoch))
    for name in ("install.sh", "install.ps1"):
        (output / name).write_bytes((ROOT / "scripts" / name).read_bytes())
    (output / "release-manifest.json").write_text(json.dumps({
        **metadata, "go": build["go"], "targets": TARGETS,
        "worktree_dirty": build["worktree_dirty"],
    }, indent=2) + "\n")
    (output / "SHA256SUMS").write_text("".join(
        hashlib.sha256(path.read_bytes()).hexdigest() + "  " + path.name + "\n"
        for path in sorted(output.iterdir()) if path.is_file()
    ))
    if not build["worktree_dirty"]:
        verify_assets(tag, output)
    print("Packaged " + tag + " in " + str(output))


def verify_assets(tag, directory):
    """核对完整附件集合、校验清单及构建提交。

    拒绝缺失、额外、重复及路径条目；下载复核也复用此函数。
    """
    expected = {"install.sh", "install.ps1", "release-manifest.json"}
    for target in TARGETS:
        expected.add("sotea-" + tag + "-" + target + (".zip" if target.startswith("windows") else ".tar.gz"))
    files = {path.name for path in directory.iterdir()}
    if files != expected | {"SHA256SUMS"}:
        raise ValueError("The release asset set is incomplete or contains unexpected files.")
    listed = set()
    for line in (directory / "SHA256SUMS").read_text().splitlines():
        match = re.fullmatch(r"([0-9a-f]{64})  ([A-Za-z0-9._-]+)", line)
        if not match or match[2] not in expected or match[2] in listed:
            raise ValueError("The release checksum manifest is invalid.")
        listed.add(match[2])
        if hashlib.sha256((directory / match[2]).read_bytes()).hexdigest() != match[1]:
            raise ValueError("Release checksum mismatch: " + match[2])
    if listed != expected:
        raise ValueError("Release checksums are missing.")
    manifest = json.loads((directory / "release-manifest.json").read_text())
    if any(manifest.get(key) != value for key, value in validate_tag(tag).items()):
        raise ValueError("Release metadata does not match the checked-out source.")
    if manifest.get("worktree_dirty") is not False or manifest.get("go") != "go1.27.3" or manifest.get("targets") != list(TARGETS):
        raise ValueError("Only clean, pinned-toolchain builds for all release targets can be published.")


def verify_native(tag, directory, target):
    """在目标 runner 验证最终归档中的实际二进制。

    校验版本、来源及原生目标，再执行帮助；此检查不代表桌面终端或完整平台验收。
    """
    verify_assets(tag, directory)
    system, architecture = target.split("-")
    native = subprocess.check_output(["go", "env", "GOOS", "GOARCH"], text=True).split()
    if native != [system, architecture]:
        raise ValueError("The runner does not match the release target.")
    folder = "sotea-" + tag + "-" + target
    binary_name = "sotea.exe" if system == "windows" else "sotea"
    if system == "windows":
        with zipfile.ZipFile(directory / (folder + ".zip")) as archive:
            binary = archive.read(folder + "/" + binary_name)
            manifest = json.loads(archive.read(folder + "/BUILD-INFO.json"))
    else:
        with tarfile.open(directory / (folder + ".tar.gz"), "r:gz") as archive:
            binary = archive.extractfile(folder + "/" + binary_name).read()
            manifest = json.load(archive.extractfile(folder + "/BUILD-INFO.json"))
    if manifest["binary_sha256"] != hashlib.sha256(binary).hexdigest() or manifest["version"] != tag:
        raise ValueError("Packaged build metadata does not match its executable.")
    with tempfile.TemporaryDirectory(prefix="sotea-release-") as temporary:
        executable = Path(temporary) / binary_name
        executable.write_bytes(binary)
        executable.chmod(0o755)
        result = json.loads(subprocess.check_output([str(executable), "version", "--json", "--no-interactive"], text=True, timeout=30))
        if result["status"] != "success" or result["items"][0]["data"]["version"] != tag:
            raise ValueError("The packaged executable reports a different version.")
        subprocess.run([str(executable), "--help"], check=True, stdout=subprocess.DEVNULL, timeout=30)
    print("Verified native archive: " + target)


def main():
    """为 Actions 提供显式验证、打包及原生检查入口。

    GitHub 输出只包含经过验证的标签、预发布标记和提交，不接受 shell 片段。
    """
    parser = argparse.ArgumentParser(description="Build and verify sotea GitHub Release assets.")
    parser.add_argument("operation", choices=("validate", "build", "verify", "native"))
    parser.add_argument("--tag", required=True)
    parser.add_argument("--directory", type=Path, default=ROOT / "dist/release")
    parser.add_argument("--target", choices=TARGETS)
    args = parser.parse_args()
    metadata = validate_tag(args.tag)
    if args.operation == "validate":
        tag_commit = subprocess.check_output(["git", "rev-parse", "refs/tags/" + args.tag + "^{commit}"], text=True).strip()
        if tag_commit != metadata["commit"]:
            raise ValueError("The checked-out commit does not match the release tag.")
        if os.environ.get("GITHUB_OUTPUT"):
            with open(os.environ["GITHUB_OUTPUT"], "a") as output:
                for key, value in metadata.items():
                    output.write(key + "=" + (str(value).lower() if isinstance(value, bool) else value) + "\n")
        print(json.dumps(metadata))
    elif args.operation == "build":
        package_release(args.tag, args.directory.resolve())
    elif args.operation == "native":
        if not args.target:
            parser.error("native requires --target")
        verify_native(args.tag, args.directory, args.target)
    else:
        verify_assets(args.tag, args.directory)


if __name__ == "__main__":
    main()
