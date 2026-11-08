#!/usr/bin/env python3
"""保护 Release 来源与附件校验的发布门槛。

仅构造小型临时附件，不构建、不联网、不创建标签或 Release。
"""

import hashlib
import importlib.util
import json
from pathlib import Path
import tempfile
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[1]


def expect_rejection(operation, message):
    """要求发布校验明确拒绝违反来源或完整性的输入。

    意外异常直接传播，避免把程序错误当成预期的保护行为。
    """
    try:
        operation()
    except ValueError as error:
        if message not in str(error):
            raise AssertionError(str(error)) from error
    else:
        raise AssertionError("An unsafe release was accepted.")


def write_manifest(directory, tag, metadata, scenario):
    """为发布验证创建最小的完整附件与摘要集合。

    dirty 和错误目标案例拥有正确摘要，确保它们在来源门槛被拒绝。
    """
    directory.mkdir()
    for target in ("darwin-arm64", "darwin-amd64", "windows-amd64"):
        suffix = ".zip" if target.startswith("windows") else ".tar.gz"
        (directory / ("sotea-" + tag + "-" + target + suffix)).write_bytes(b"fixture archive")
    for name in ("install.sh", "install.ps1"):
        (directory / name).write_bytes(b"fixture installer")
    manifest = {**metadata, "go": "go1.27.3", "targets": ["darwin-arm64", "darwin-amd64", "windows-amd64"], "worktree_dirty": False}
    if scenario == "dirty":
        manifest["worktree_dirty"] = True
    elif scenario == "wrong-source":
        manifest["commit"] = "0" * 40
    elif scenario == "wrong-toolchain":
        manifest["go"] = "go1.26.10"
    elif scenario == "wrong-target":
        manifest["targets"] = ["windows-amd64"]
    (directory / "release-manifest.json").write_text(json.dumps(manifest))
    checksums = "".join(hashlib.sha256(path.read_bytes()).hexdigest() + "  " + path.name + "\n" for path in sorted(directory.iterdir()))
    (directory / "SHA256SUMS").write_text(checksums)
    if scenario == "tampered":
        (directory / "install.sh").write_bytes(b"changed")
    elif scenario == "duplicate":
        (directory / "SHA256SUMS").write_text(checksums + checksums.splitlines()[0] + "\n")
    elif scenario == "missing":
        (directory / "install.ps1").unlink()
    elif scenario == "unexpected":
        (directory / "extra-file").write_bytes(b"extra")


def main():
    """验证 SemVer 通道、VERSION 和来源／附件拒绝策略。

    夹具均为合成字节；这些检查不代表真实归档或系统运行验收。
    """
    spec = importlib.util.spec_from_file_location("sotea_release", ROOT / "scripts/release.py")
    release = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(release)
    tag = (ROOT / "cmd/sotea/VERSION").read_text().strip()
    metadata = release.validate_tag(tag)
    assert metadata["prerelease"] == ("-" in tag)
    for invalid in ("v01.2.3", "v1.2.3-rc.01", "v1.2.3+ci.1", "../tag", "v1.2", "v1.2.3\n"):
        expect_rejection(lambda value=invalid: release.validate_tag(value), "Expected a release tag")
    different = "v99.0.0" if tag != "v99.0.0" else "v98.0.0"
    expect_rejection(lambda: release.validate_tag(different), "exactly match")
    with tempfile.TemporaryDirectory(prefix="sotea-release-check-") as temporary:
        base = Path(temporary)
        source = base / "source"
        (source / "cmd/sotea").mkdir(parents=True)
        for candidate in ("v1.2.3", "v1.2.3-rc.10"):
            (source / "cmd/sotea/VERSION").write_text(candidate)
            with patch.object(release, "ROOT", source), patch.object(release.subprocess, "check_output", return_value=metadata["commit"]):
                assert release.validate_tag(candidate)["prerelease"] == ("-" in candidate)
        scenarios = {
            "success": None, "dirty": "Only clean", "wrong-source": "does not match",
            "wrong-toolchain": "Only clean", "wrong-target": "Only clean",
            "tampered": "checksum mismatch", "duplicate": "manifest is invalid",
            "missing": "incomplete", "unexpected": "incomplete",
        }
        for scenario, error in scenarios.items():
            directory = base / scenario
            write_manifest(directory, tag, metadata, scenario)
            if error:
                expect_rejection(lambda: release.verify_assets(tag, directory), error)
            else:
                release.verify_assets(tag, directory)
            print("PASS release: " + scenario)
    print("PASS release: SemVer, VERSION and stable/prerelease channels")


if __name__ == "__main__":
    main()
