#!/usr/bin/env python3
"""把已验证的 P7 二进制装入可核验的内部候选包。

不重新编译、签名或发布；来源／文件发生变化时拒绝复用旧证据或覆盖同名不同内容的包。
"""

import argparse
from datetime import datetime, timezone
import gzip
import hashlib
import io
import json
from pathlib import Path
import struct
import subprocess
import tarfile
import zipfile

ROOT = Path(__file__).resolve().parents[1]
LICENSE_PREFIXES = ("LICENSE", "LICENCE", "COPYING", "NOTICE", "COPYRIGHT", "PATENTS")


def digest(contents):
    """计算最终字节的 SHA-256。

    清单与压缩包校验使用同一编码，不能根据文件名推断内容相同。
    """
    return hashlib.sha256(contents).hexdigest()


def license_files(module, directory):
    """收集模块原有许可及附加声明，并保留模块内相对路径。

    无独立 LICENSE 的 localereader 使用原 README 和已固定上游提交的补充文件。
    """
    files = {str(path.relative_to(directory)): path.read_bytes()
             for path in sorted(directory.rglob("*"))
             if path.is_file() and path.name.upper().startswith(LICENSE_PREFIXES)}
    if module == "github.com/mattn/go-localereader":
        files["README.md"] = (directory / "README.md").read_bytes()
        supplement = ROOT / "third-party/go-localereader"
        provenance = json.loads((supplement / "provenance.json").read_text())
        contents = (supplement / "LICENSE").read_bytes()
        if digest(contents) != provenance["sha256"]:
            raise RuntimeError("The pinned supplementary license has changed.")
        files["upstream-LICENSE"] = contents
        files["provenance.json"] = (supplement / "provenance.json").read_bytes()
    if not files:
        raise RuntimeError("Missing dependency license: " + module)
    return files


def dependencies(artifacts):
    """按三个实际二进制的 Go buildinfo 收集链接模块及校验和。

    仅用本地已锁定模块目录，不升级依赖；平台独有依赖也纳入统一声明。
    """
    linked = {}
    for artifact in artifacts:
        binary = ROOT / artifact["path"]
        output = subprocess.check_output(["go", "version", "-m", str(binary)], text=True)
        for line in output.splitlines():
            fields = line.split()
            if fields and fields[0] == "dep":
                module, version, checksum = fields[1:4]
                item = linked.setdefault(module, {"module": module, "version": version, "sum": checksum, "targets": []})
                if (item["version"], item["sum"]) != (version, checksum):
                    raise RuntimeError("Candidate binaries use different dependency versions.")
                item["targets"].append(artifact["os"] + "/" + artifact["arch"])
    bundled = {}
    for module, item in sorted(linked.items()):
        info = json.loads(subprocess.check_output(["go", "list", "-m", "-json", module], text=True))
        if info["Version"] != item["version"] or info.get("Sum") != item["sum"]:
            raise RuntimeError("Module cache does not match the candidate build.")
        prefix = "LICENSES/" + module + "@" + item["version"] + "/"
        files = license_files(module, Path(info["Dir"]))
        item["license_files"] = {prefix + name: digest(contents) for name, contents in files.items()}
        bundled.update({prefix + name: contents for name, contents in files.items()})
    goroot = Path(subprocess.check_output(["go", "env", "GOROOT"], text=True).strip())
    for path in [goroot / "LICENSE", goroot / "PATENTS", *(goroot / "src/vendor").rglob("*")]:
        if path.is_file() and path.name.upper().startswith(LICENSE_PREFIXES):
            bundled["LICENSES/go1.27.3/" + str(path.relative_to(goroot))] = path.read_bytes()
    return list(linked.values()), bundled


def archive_bytes(files, folder, system, epoch):
    """生成固定顺序、时间和文件权限的单根压缩包。

    macOS 为 tar.gz，Windows 为 zip；输入只包含显式收集的文件，不遍历工作区或凭证目录。
    """
    output = io.BytesIO()
    if system == "windows":
        stamp = datetime.fromtimestamp(epoch, timezone.utc).timetuple()[:6]
        with zipfile.ZipFile(output, "w", compression=zipfile.ZIP_DEFLATED) as archive:
            for name, contents in sorted(files.items()):
                info = zipfile.ZipInfo(folder + "/" + name, stamp)
                info.create_system = 3
                info.external_attr = (0o100644 << 16)
                archive.writestr(info, contents, compress_type=zipfile.ZIP_DEFLATED)
    else:
        with gzip.GzipFile(fileobj=output, mode="wb", filename="", mtime=epoch) as compressed:
            with tarfile.open(fileobj=compressed, mode="w") as archive:
                for name, contents in sorted(files.items()):
                    info = tarfile.TarInfo(folder + "/" + name)
                    info.size = len(contents)
                    info.mtime = epoch
                    info.mode = 0o755 if name == "sotea" else 0o644
                    archive.addfile(info, io.BytesIO(contents))
    return output.getvalue()


def signature_status(artifact):
    """读取现有文件的签名证据，不为候选增加或修改签名。

    Mach-O 通过 codesign 核查；PE 检查证书目录，存在证书时不冒充已验证的 Authenticode。
    """
    path = ROOT / artifact["path"]
    if artifact["os"] == "darwin":
        result = subprocess.run(["codesign", "-dv", "--verbose=2", str(path)], capture_output=True, text=True)
        if result.returncode == 0 and "Signature=adhoc" in result.stderr:
            return "Go linker ad-hoc signature only; no Developer ID or notarization"
        if result.returncode != 0 and "not signed at all" in result.stderr:
            return "Unsigned Mach-O; no Developer ID or notarization"
        raise RuntimeError("Unexpected signing state; review the candidate before packaging.")
    contents = path.read_bytes()
    pe = struct.unpack_from("<I", contents, 0x3C)[0]
    if contents[pe:pe + 4] != b"PE\x00\x00" or struct.unpack_from("<H", contents, pe + 24)[0] != 0x20B:
        raise RuntimeError("Expected a PE32+ Windows candidate.")
    certificate = struct.unpack_from("<II", contents, pe + 24 + 112 + 4 * 8)
    if certificate != (0, 0):
        raise RuntimeError("Unexpected PE certificate; verify Authenticode before packaging.")
    return "Unsigned PE; no Authenticode certificate"


def main():
    """验证构建来源后打包，并生成摘要与独立交付清单。

    打包公开 docs/ 使用文档，清单写入本地 devdocs；不把内部设计资料加入交付包。
    """
    parser = argparse.ArgumentParser(description="Package the existing P7 candidate without rebuilding it.")
    parser.add_argument("--build", type=Path, default=ROOT / "dist/p7/build-info.json")
    parser.add_argument("--output", type=Path, default=ROOT / "dist/p7/packages")
    parser.add_argument("--package-revision", type=int, required=True, help="Identify this packaging revision without rebuilding the executable")
    args = parser.parse_args()
    if args.package_revision < 1:
        parser.error("The package revision must be positive.")
    args.output = args.output.resolve()
    if not args.output.is_relative_to(ROOT):
        parser.error("The candidate package directory must be inside the workspace for relative evidence paths.")
    build = json.loads(args.build.read_text())
    if build["go"] != "go1.27.3" or subprocess.check_output(["go", "env", "GOVERSION"], text=True).strip() != build["go"]:
        raise RuntimeError("Packaging requires the candidate Go 1.27.3 toolchain.")
    current = {str(path.relative_to(ROOT)): digest(path.read_bytes()) for path in
               [ROOT / "go.mod", ROOT / "go.sum", *(ROOT / "cmd").rglob("*.go"), *(ROOT / "internal").rglob("*.go")]}
    if current != build["source_sha256"]:
        raise RuntimeError("Source files no longer match the candidate build.")
    for artifact in build["artifacts"]:
        if digest((ROOT / artifact["path"]).read_bytes()) != artifact["sha256"]:
            raise RuntimeError("A candidate binary changed after validation.")
    modules, licenses = dependencies(build["artifacts"])
    notices = {"schema_version": 1, "modules": modules,
               "go_runtime": "Go 1.27.3; original license and bundled vendor notices in LICENSES/go1.27.3",
               "localereader_note": "v0.0.1 declares MIT in README; original declaration and later upstream license preserved with provenance"}
    user_docs = {path.name: path.read_bytes() for path in sorted((ROOT / "docs").glob("*.md"))}
    required_docs = {"README.md", "installation.md", "configuration.md", "usage.md", "mcp.md", "compatibility.md", "windows-validation.md"}
    if not required_docs.issubset(user_docs):
        raise RuntimeError("Required user documentation is missing.")
    common = {"LICENSE": (ROOT / "LICENSE").read_bytes(), **user_docs,
              "THIRD-PARTY-NOTICES.json": (json.dumps(notices, indent=2) + "\n").encode(), **licenses}
    epoch = int(datetime.fromisoformat(build["time_utc"]).timestamp())
    records = []
    args.output.mkdir(parents=True, exist_ok=True)
    for artifact in build["artifacts"]:
        system, arch = artifact["os"], artifact["arch"]
        name = "sotea.exe" if system == "windows" else "sotea"
        folder = "sotea-" + build["version"] + "-pkg" + str(args.package_revision) + "-" + system + "-" + arch
        manifest = {"schema_version": 1, "version": build["version"], "commit": build["commit"],
                    "go": build["go"], "build_options": build["build_options"], "os": system, "arch": arch,
                    "package_revision": args.package_revision,
                    "binary_sha256": artifact["sha256"], "source_sha256": build["source_sha256"],
                    "distribution": "internal acceptance candidate; not approved for P8 publication",
                    "signature": signature_status(artifact)}
        files = dict(common)
        if system == "windows":
            # Windows PowerShell 5.1 需要 BOM 才能稳定识别包含中文注释的 UTF-8 脚本。
            # 发布包保留原始脚本内容，编码转换后的字节参与 CONTENTS.sha256。
            files["windows-p7-smoke.ps1"] = (ROOT / "scripts/windows-p7-smoke.ps1").read_text().encode("utf-8-sig")
        files[name] = (ROOT / artifact["path"]).read_bytes()
        files["BUILD-INFO.json"] = (json.dumps(manifest, indent=2) + "\n").encode()
        files["CONTENTS.sha256"] = "".join(digest(data) + "  " + path + "\n" for path, data in sorted(files.items())).encode()
        contents = archive_bytes(files, folder, system, epoch)
        target = args.output / (folder + (".zip" if system == "windows" else ".tar.gz"))
        if target.exists() and target.read_bytes() != contents:
            raise RuntimeError("Refusing to replace a different package with the same candidate name: " + target.name)
        target.write_bytes(contents)
        records.append({"path": str(target.relative_to(ROOT)), "sha256": digest(contents),
                        "binary_sha256": artifact["sha256"], "os": system, "arch": arch,
                        "files": len(files), "signature": manifest["signature"]})
        print("Packaged " + target.name, flush=True)
    (args.output / "SHA256SUMS").write_text("".join(item["sha256"] + "  " + Path(item["path"]).name + "\n" for item in records))
    record = {"schema_version": 1, "time_utc": datetime.now(timezone.utc).isoformat(), "version": build["version"],
              "package_revision": args.package_revision,
              "status": "packaged_pending_acceptance", "modules": len(modules), "artifacts": records}
    validation = ROOT / "devdocs/validation"
    validation.mkdir(parents=True, exist_ok=True)
    (validation / "p7-packages-result.json").write_text(json.dumps(record, indent=2) + "\n")


if __name__ == "__main__":
    main()
