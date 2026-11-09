#!/usr/bin/env python3
"""在临时目录验证首次安装的完整性与不覆盖边界。

用下载命令替身提供固定附件，不访问外网、不修改真实 HOME／用户 PATH。
"""

import argparse
import hashlib
import io
import os
from pathlib import Path
import platform
import subprocess
import sys
import tarfile
import tempfile
import zipfile

ROOT = Path(__file__).resolve().parents[1]


def make_fixture(directory, tag, target, binary=None, scenario="success"):
    """制作单场景附件，只在成功场景使用已打包的真实程序。

    篡改只发生在隔离副本；失败场景分别保护摘要、归档条目及版本边界。
    """
    directory.mkdir()
    folder = "sotea-" + tag + "-" + target
    if target.startswith("windows"):
        archive = directory / (folder + ".zip")
        with zipfile.ZipFile(archive, "w") as stream:
            stream.writestr(folder + "/sotea.exe", binary)
    else:
        archive = directory / (folder + ".tar.gz")
        if binary is None:
            reported = "v0.0.0" if scenario == "wrong-version" else tag
            binary = ("#!/bin/sh\nprintf '%s\\n' 'sotea " + reported + "'\n").encode()
        with tarfile.open(archive, "w:gz") as stream:
            entry = tarfile.TarInfo(folder + "/sotea")
            entry.mode = 0o755
            if scenario == "symlink":
                entry.type = tarfile.SYMTYPE
                entry.linkname = "/bin/sh"
                stream.addfile(entry)
            else:
                entry.size = len(binary)
                stream.addfile(entry, io.BytesIO(binary))
    digest = hashlib.sha256(archive.read_bytes()).hexdigest()
    if scenario == "checksum":
        digest = "0" * 64
    checksum = digest + "  " + archive.name + "\n"
    if scenario == "duplicate-checksum":
        checksum *= 2
    (directory / "SHA256SUMS").write_text(checksum)
    return archive


def run_shell_tests(tag, release_directory):
    """运行 sh 的安装、失败保留、路径与通道场景。

    Linux 使用模拟 Darwin 标识和小型程序；macOS 发布任务同时验证真实附件安装。
    """
    architecture = "arm64" if platform.machine() == "arm64" else "amd64"
    target = "darwin-" + architecture
    binary = None
    if release_directory:
        folder = "sotea-" + tag + "-" + target
        with tarfile.open(release_directory / (folder + ".tar.gz"), "r:gz") as archive:
            binary = archive.extractfile(folder + "/sotea").read()
    scenarios = ["success", "checksum", "duplicate-checksum", "symlink", "existing", "download", "latest", "invalid-tag", "path", "bash-profile", "destination-race"]
    if binary is None:
        scenarios.append("wrong-version")
    with tempfile.TemporaryDirectory(prefix="sotea-installer-tests-") as temporary:
        base = Path(temporary)
        helpers = base / "helpers"
        helpers.mkdir()
        curl = helpers / "curl"
        curl.write_text("#!" + sys.executable + "\n" + '''import os, sys, shutil
from pathlib import Path
args = sys.argv[1:]
fixture = Path(os.environ['SOTEA_INSTALL_FIXTURE'])
url = next(value for value in args if value.startswith('https://'))
with (fixture / 'requests').open('a') as output:
    output.write(url + '\\n')
repo = 'https://github.com/Spring-Ocean/gitea-cli-so'
if url == repo + '/releases/latest':
    print(repo + '/releases/tag/' + os.environ['SOTEA_INSTALL_TAG'], end='')
    sys.exit(0)
prefix = repo + '/releases/download/' + os.environ['SOTEA_INSTALL_TAG'] + '/'
if not url.startswith(prefix) or os.environ.get('SOTEA_INSTALL_DOWNLOAD_FAIL') == '1':
    sys.exit(22)
name = url[len(prefix):]
if '/' in name or not (fixture / name).is_file():
    sys.exit(22)
shutil.copyfile(fixture / name, args[args.index('--output') + 1])
if name == 'SHA256SUMS' and os.environ.get('SOTEA_INSTALL_RACE_TARGET'):
    destination = Path(os.environ['SOTEA_INSTALL_RACE_TARGET'])
    destination.mkdir(parents=True)
    (destination / 'keep-existing').write_bytes(b'keep-existing')
''')
        curl.chmod(0o755)
        uname = helpers / "uname"
        uname.write_text("#!/bin/sh\ncase \"$1\" in\n-s) echo Darwin ;;\n-m) echo " + ("arm64" if architecture == "arm64" else "x86_64") + " ;;\n*) exit 1 ;;\nesac\n")
        uname.chmod(0o755)
        for scenario in scenarios:
            fixture = base / scenario
            # latest 测试可传入预发布标签，下载地址仍必须固定为解析后的同一版本。
            # 发布时用户未指定标签只会从 GitHub 的稳定版入口解析版本。
            make_fixture(fixture, tag, target, binary=binary, scenario=scenario)
            home = fixture / "home with spaces"
            home.mkdir()
            install_directory = home / (".local/bin" if scenario in {"path", "bash-profile"} else "custom bin")
            target_path = install_directory / "sotea"
            if scenario == "existing":
                install_directory.mkdir()
                target_path.write_bytes(b"keep-existing")
            environment = dict(os.environ, HOME=str(home), SHELL="/bin/zsh", PATH=str(helpers) + os.pathsep + os.environ["PATH"],
                               SOTEA_INSTALL_FIXTURE=str(fixture), SOTEA_INSTALL_TAG=tag)
            if scenario == "download":
                environment["SOTEA_INSTALL_DOWNLOAD_FAIL"] = "1"
            if scenario == "bash-profile":
                environment["SHELL"] = "/bin/bash"
                (home / ".profile").write_text("# keep existing profile\n")
            if scenario == "destination-race":
                environment["SOTEA_INSTALL_RACE_TARGET"] = str(target_path)
            arguments = ["sh", str(ROOT / "scripts/install.sh")]
            if scenario != "latest":
                arguments += ["--version", "../unsafe" if scenario == "invalid-tag" else tag]
            if scenario not in {"path", "bash-profile"}:
                arguments += ["--install-dir", str(install_directory), "--no-modify-path"]
            result = subprocess.run(arguments, env=environment, capture_output=True, text=True, timeout=60)
            success = scenario in {"success", "latest", "path", "bash-profile"}
            if (result.returncode == 0) != success:
                raise AssertionError(scenario + ": " + result.stdout + result.stderr)
            if success:
                assert target_path.is_file(), scenario
                assert subprocess.check_output([str(target_path), "version"], text=True).strip() == "sotea " + tag
            elif scenario == "existing":
                assert target_path.read_bytes() == b"keep-existing"
                assert not (fixture / "requests").exists()
            elif scenario == "destination-race":
                assert target_path.is_dir()
                assert list(target_path.iterdir()) == [target_path / "keep-existing"]
                assert (target_path / "keep-existing").read_bytes() == b"keep-existing"
            else:
                assert not target_path.exists(), scenario
            if scenario == "path":
                assert (home / ".zshrc").read_text().count('export PATH="$HOME/.local/bin:$PATH"') == 1
            else:
                assert not (home / ".zshrc").exists(), scenario
            if scenario == "bash-profile":
                assert not (home / ".bash_profile").exists()
                profile = (home / ".profile").read_text()
                assert profile.startswith("# keep existing profile\n")
                assert profile.count('export PATH="$HOME/.local/bin:$PATH"') == 1
            assert not list(install_directory.glob(".sotea-install.*")), scenario
            print("PASS sh: " + scenario)


def run_windows_tests(tag, release_directory, powershell):
    """用两种真实 PowerShell 执行 ZIP 安装及失败边界。

    先验证实际文档的下载文本命令；网络替身和 NoModifyPath 隔离真实配置。
    """
    subprocess.run([
        powershell, "-NoProfile", "-NonInteractive", "-File", str(ROOT / "scripts/test-install-command.ps1"),
        "-InstallerPath", str((release_directory / "install.ps1").resolve()),
    ], check=True, timeout=60)
    folder = "sotea-" + tag + "-windows-amd64"
    with zipfile.ZipFile(release_directory / (folder + ".zip")) as archive:
        binary = archive.read(folder + "/sotea.exe")
    harness = ROOT / "scripts/test-installers.ps1"
    for scenario in ("success", "downloaded-script", "checksum", "duplicate-checksum", "existing", "download", "latest", "wrong-version", "invalid-tag"):
        with tempfile.TemporaryDirectory(prefix="sotea-installer-tests-") as temporary:
            fixture = Path(temporary) / "fixture"
            make_fixture(fixture, tag, "windows-amd64", binary=binary, scenario=scenario)
            install_directory = fixture / "install with spaces"
            target = install_directory / "sotea.exe"
            if scenario == "existing":
                install_directory.mkdir()
                target.write_bytes(b"keep-existing")
            environment = dict(os.environ, SOTEA_INSTALL_FIXTURE=str(fixture), SOTEA_INSTALL_TAG=tag,
                               SOTEA_INSTALL_SCENARIO=scenario, SOTEA_INSTALL_TARGET=str(install_directory))
            result = subprocess.run([powershell, "-NoProfile", "-NonInteractive", "-File", str(harness)],
                                    env=environment, capture_output=True, text=True, timeout=90)
            success = scenario in {"success", "downloaded-script", "latest"}
            if (result.returncode == 0) != success:
                raise AssertionError(scenario + ": " + result.stdout + result.stderr)
            if success:
                assert target.is_file()
                assert subprocess.check_output([str(target), "version"], text=True).strip() == "sotea " + tag
            elif scenario == "existing":
                assert target.read_bytes() == b"keep-existing"
                assert not (fixture / "requests").exists()
            else:
                assert not target.exists(), scenario
            assert not list(install_directory.glob(".sotea-install-*")), scenario
            print("PASS " + powershell + ": " + scenario)


def main():
    """按 runner 选择安装器并要求 Windows 使用已构建的真实附件。

    默认 sh 测试不依赖 Go 或远端；Windows 必须显式给出包目录与解释器。
    """
    parser = argparse.ArgumentParser(description="Test first-install scripts without network or real user configuration.")
    parser.add_argument("--directory", type=Path)
    parser.add_argument("--tag", default="v0.1.0-rc.1")
    parser.add_argument("--powershell")
    args = parser.parse_args()
    if os.name == "nt":
        if not args.directory or not args.powershell:
            parser.error("Windows requires --directory and --powershell.")
        run_windows_tests(args.tag, args.directory, args.powershell)
    else:
        run_shell_tests(args.tag, args.directory)


if __name__ == "__main__":
    main()
