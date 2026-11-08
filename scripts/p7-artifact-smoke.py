#!/usr/bin/env python3
"""核验最终压缩包，并在临时 macOS 目录演练安装、升级、恢复和卸载。

不安装到开发者 PATH、不改真实配置；外平台只检查归档字节，不标记运行通过。
"""

import argparse
from datetime import datetime, timezone
import hashlib
import importlib.util
import json
import os
from pathlib import Path, PurePosixPath
import platform
import shutil
import subprocess
import tarfile
import tempfile
import zipfile

ROOT = Path(__file__).resolve().parents[1]


def sha(path):
    """计算实存文件摘要，用于程序替换前后的字节核对。

    不读取配置以外的用户文件；所有路径来自候选清单或临时测试根。
    """
    return hashlib.sha256(path.read_bytes()).hexdigest()


def unpack(artifact, directory):
    """核查归档路径和文件清单后解压，拒绝链接及越界条目。

    内部 CONTENTS.sha256 逐字节验证；配置、凭证或额外文件都不能混入候选包。
    """
    source = ROOT / artifact["path"]
    assert sha(source) == artifact["sha256"]
    files = {}
    modes = {}
    if source.suffix == ".zip":
        with zipfile.ZipFile(source) as archive:
            for entry in archive.infolist():
                assert entry.filename not in files and not entry.is_dir()
                files[entry.filename] = archive.read(entry)
                modes[entry.filename] = 0o644
    else:
        with tarfile.open(source, "r:gz") as archive:
            for entry in archive:
                assert entry.isfile() and entry.name not in files
                files[entry.name] = archive.extractfile(entry).read()
                modes[entry.name] = entry.mode
    roots = set()
    for name, content in files.items():
        parts = PurePosixPath(name)
        assert not parts.is_absolute() and ".." not in parts.parts and len(parts.parts) >= 2
        roots.add(parts.parts[0])
        path = directory.joinpath(*parts.parts)
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_bytes(content)
        path.chmod(modes[name])
    assert len(roots) == 1 and len(files) == artifact["files"]
    folder = directory / roots.pop()
    listed = {}
    for line in (folder / "CONTENTS.sha256").read_text().splitlines():
        expected, name = line.split("  ", 1)
        assert name not in listed and sha(folder / name) == expected
        listed[name] = expected
    actual = {str(path.relative_to(folder)) for path in folder.rglob("*") if path.is_file()}
    assert actual == set(listed) | {"CONTENTS.sha256"}
    assert "Copyright (c) 2026 springocean" in (folder / "LICENSE").read_text()
    assert {"BUILD-INFO.json", "installation.md", "THIRD-PARTY-NOTICES.json"} <= actual
    notices = json.loads((folder / "THIRD-PARTY-NOTICES.json").read_text())
    for module in notices["modules"]:
        for name, expected in module["license_files"].items():
            assert sha(folder / name) == expected
    name = "sotea.exe" if artifact["os"] == "windows" else "sotea"
    binary = folder / name
    assert sha(binary) == artifact["binary_sha256"]
    if artifact["os"] == "darwin":
        assert os.access(binary, os.X_OK)
    return folder, binary


def macos_lifecycle(binary, previous, directory):
    """使用解压后的原始程序演练中文／空格路径和程序替换。

    失败停在同目录 staging；原程序、配置及未知版本配置在失败后保持原字节。
    """
    workspace = directory / "project 中文 space"
    workspace.mkdir()
    install = directory / "home/.local/bin"
    install.mkdir(parents=True)
    environment = dict(os.environ, HOME=str(directory / "home"), CI="1", GIT_CONFIG_NOSYSTEM="1",
                       GIT_CONFIG_GLOBAL=os.devnull, GIT_CONFIG_COUNT="0", PATH=str(install) + os.pathsep + os.environ["PATH"])
    for name in list(environment):
        if name.startswith("SOTEA_"):
            environment.pop(name)
    configuration = directory / "config.json"
    configuration.write_text(json.dumps({"version": 1, "revision": 7, "default_instance": "team",
        "instances": {"team": {"url": "http://127.0.0.1:1", "proxy": "direct", "active_account": "alice",
            "accounts": {"alice": {"source": "environment", "instance_url": "http://127.0.0.1:1"}}}}, "repositories": []}))
    baseline = configuration.read_bytes()
    target = install / "sotea"
    subprocess.run(["install", "-m", "755", str(previous), str(target)], env=environment, check=True)
    old_hash = sha(target)
    old_version = subprocess.check_output([str(target), "version"], env=environment, text=True).strip()
    command = [str(target), "--config", str(configuration), "--workdir", str(workspace), "--instance", "team", "--json", "auth", "status"]
    assert json.loads(subprocess.check_output(command, env=environment))["status"] == "success"
    upgrade = 'set -e\ninstall -m 755 "$1" "$2.new"\n"$2.new" version\ncp -p "$2" "$2.previous"\nmv -f "$2.new" "$2"\n"$2" version\n'
    failed = subprocess.run(["/bin/sh", "-c", upgrade, "sotea-upgrade", str(directory / "missing"), str(target)],
                            env=environment, capture_output=True)
    assert failed.returncode != 0 and sha(target) == old_hash and configuration.read_bytes() == baseline
    subprocess.run(["/bin/sh", "-c", upgrade, "sotea-upgrade", str(binary), str(target)],
                   env=environment, capture_output=True, check=True)
    assert sha(target) == sha(binary) and sha(Path(str(target) + ".previous")) == old_hash
    version = subprocess.check_output(["sotea", "version"], cwd=workspace, env=environment, text=True).strip()
    assert version == subprocess.check_output([str(binary), "version"], env=environment, text=True).strip()
    assert json.loads(subprocess.check_output(command, env=environment))["status"] == "success"
    assert configuration.read_bytes() == baseline
    configuration.write_text('{"version":2}\n')
    incompatible = configuration.read_bytes()
    rejected = subprocess.run(command, env=environment, capture_output=True)
    assert rejected.returncode != 0 and json.loads(rejected.stdout)["error"]["code"] == "CONFIG_ERROR"
    assert configuration.read_bytes() == incompatible
    configuration.write_bytes(baseline)
    subprocess.run(["git", "-C", str(workspace), "init", "--quiet", "--initial-branch=main"], env=environment, check=True)
    git = subprocess.check_output([str(target), "--workdir", str(workspace), "git", "--", "rev-parse", "--show-toplevel"],
                                  env=environment, text=True).strip()
    assert Path(git).resolve() == workspace.resolve()
    spec = importlib.util.spec_from_file_location("p7_protocol", ROOT / "scripts/p6-mcp-smoke.py")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    environment.update(SOTEA_INSTANCE="team", SOTEA_REPO="team/repo")
    client = module.Client(target, configuration, environment, workspace, write=False, raw=False)
    try:
        assert len(client.request("tools/list", {})["tools"]) == 28
        client.tool("context_get")
    finally:
        client.close()
    # 恢复走同目录 staging 和 rename，不能先删除正在安装的程序。
    staged = Path(str(target) + ".new")
    shutil.copy2(Path(str(target) + ".previous"), staged)
    subprocess.run([str(staged), "version"], env=environment, capture_output=True, check=True)
    staged.replace(target)
    assert sha(target) == old_hash and configuration.read_bytes() == baseline
    target.unlink()
    assert not target.exists() and configuration.read_bytes() == baseline
    return {"status": "passed", "previous_version": old_version, "previous_sha256": old_hash,
            "installed_version": version, "configuration_format": 1,
            "checks": ["archive_content_hashes", "user_install_and_path", "space_unicode_path", "git_passthrough",
                       "mcp_28_tools_context_and_eof", "development_binary_upgrade_preserves_config", "failed_staging_keeps_old_program",
                       "unsupported_config_rejected_unchanged", "backup_rollback", "uninstall_preserves_config"],
            "scope": "macOS local filesystem and synthetic configuration; no Gatekeeper download or Windows execution"}


def main():
    """对三种归档进行静态验证，并仅运行本机匹配架构。

    读取 devdocs 中的打包清单并保存验收结果；不把解压或跨平台构建当作原生运行。
    """
    parser = argparse.ArgumentParser(description="Verify P7 archives and isolated macOS installation lifecycle.")
    parser.add_argument("--previous", type=Path, default=ROOT / "dist/p6-workspace/darwin-arm64/sotea")
    args = parser.parse_args()
    record = json.loads((ROOT / "devdocs/validation/p7-packages-result.json").read_text())
    results = []
    with tempfile.TemporaryDirectory(prefix="sotea-p7-install-") as temporary:
        directory = Path(temporary)
        for artifact in record["artifacts"]:
            _, binary = unpack(artifact, directory)
            result = {"os": artifact["os"], "arch": artifact["arch"], "archive_sha256": artifact["sha256"],
                      "binary_sha256": artifact["binary_sha256"], "archive_validation": "passed", "runtime": "not_tested"}
            if artifact["os"] == "darwin" and platform.system() == "Darwin" and artifact["arch"] == platform.machine():
                result["runtime"] = macos_lifecycle(binary, args.previous.resolve(), directory)
            results.append(result)
    output = {"schema_version": 1, "time_utc": datetime.now(timezone.utc).isoformat(),
              "artifacts": results, "pending": ["Windows 10/11 installation and File.Replace", "Intel and minimum macOS", "Downloaded package OS trust checks"]}
    (ROOT / "devdocs/validation/p7-artifacts-result.json").write_text(json.dumps(output, indent=2) + "\n")
    print("All archive contents verified; native macOS install, upgrade, rollback, MCP and config preservation passed.")


if __name__ == "__main__":
    main()
