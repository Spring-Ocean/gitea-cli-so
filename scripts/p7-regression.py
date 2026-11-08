#!/usr/bin/env python3
"""对同一 P7 候选执行七版本认证、CLI 与 MCP 回归。

复用现有隔离夹具和业务场景，不访问团队实例；每版完成后仅移除本项目容器，保留卷。
"""

import argparse
from datetime import datetime, timezone
import hashlib
import importlib.util
import json
from pathlib import Path
import traceback

ROOT = Path(__file__).resolve().parents[1]


def load(name, filename):
    """加载已有阶段脚本的真实业务验证函数。

    不调用其 main，避免覆盖历史阶段的证据文件。
    """
    spec = importlib.util.spec_from_file_location(name, ROOT / "scripts" / filename)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def main():
    """串行执行版本矩阵并逐场景保存结果，失败不标为已通过。

    结果写入自动创建的 devdocs/validation；完整异常留在隔离状态目录，不回显凭证。
    """
    parser = argparse.ArgumentParser(description="Run isolated P7 candidate regression.")
    parser.add_argument("--binary", type=Path, default=ROOT / "dist/p7/darwin-arm64/sotea")
    parser.add_argument("--versions", nargs="+")
    parser.add_argument("--resume", action="store_true", help="Keep passed results for the same binary and rerun interrupted or failed versions")
    args = parser.parse_args()
    binary = args.binary.resolve()
    fixture = load("p7_environment", "p0-environment.py")
    versions = args.versions or list(fixture.VERSIONS)
    if any(version not in fixture.VERSIONS for version in versions):
        parser.error("Only the seven isolated Gitea versions are allowed.")
    stages = [("authentication_repository", load("p7_readonly", "p2-readonly-smoke.py")),
              ("issue_raw_api", load("p7_issue", "p3-issue-smoke.py")),
              ("pr_release", load("p7_workflow", "p4-workflow-smoke.py")),
              ("mcp", load("p7_mcp", "p6-mcp-smoke.py"))]
    output = ROOT / "devdocs/validation/p7-regression-result.json"
    output.parent.mkdir(parents=True, exist_ok=True)
    record = {"schema_version": 1, "time_utc": datetime.now(timezone.utc).isoformat(),
              "binary_sha256": hashlib.sha256(binary.read_bytes()).hexdigest(),
              "scope": "Isolated loopback instances; macOS arm64 CLI and stdio, not target OS/client UI acceptance",
              "status": "running", "versions": []}
    if args.resume and output.exists():
        previous = json.loads(output.read_text())
        if previous["binary_sha256"] != record["binary_sha256"]:
            parser.error("Cannot reuse evidence from another candidate binary.")
        archive = output.with_name("p7-regression-interrupted-" + datetime.now(timezone.utc).strftime("%Y%m%dT%H%M%SZ") + ".json")
        archive.write_text(json.dumps(previous, indent=2) + "\n")
        record["previous_attempt"] = str(archive.relative_to(ROOT))
        record["versions"] = [entry for entry in previous["versions"]
                              if entry["status"] == "passed" and entry["version"] in versions]
    failed = False
    try:
        for version in versions:
            if any(entry["version"] == version for entry in record["versions"]):
                print(f"{version}: retained passed evidence for the identical binary.", flush=True)
                continue
            entry = {"version": version, "status": "running", "stages": {}}
            record["versions"].append(entry)
            service = "gitea-" + version.replace(".", "-")
            try:
                print(f"{version}: preparing fixed isolated fixture.", flush=True)
                environment = fixture.prepare(version, 1)
                if environment["status"] != "passed":
                    raise RuntimeError("Isolated fixture preparation failed.")
                entry["server"] = {key: environment[key] for key in
                                   ("reported_version", "image_id", "digests", "architecture")}
                for stage, module in stages:
                    print(f"{version}: {stage}.", flush=True)
                    entry["active_stage"] = stage
                    output.write_text(json.dumps(record, indent=2) + "\n")
                    if stage == "authentication_repository":
                        result = module.smoke(binary, version, version == "28.3.0")
                    else:
                        result = module.smoke(binary, version)
                    entry["stages"][stage] = result
                entry.pop("active_stage", None)
                entry["status"] = "passed"
                print(f"{version}: all candidate workflows passed.", flush=True)
            except Exception as error:
                failed = True
                entry["status"] = "failed"
                entry["error_type"] = type(error).__name__
                (fixture.STATE / f"p7-failure-{version}.txt").write_text(traceback.format_exc())
                (fixture.STATE / f"p7-failure-{version}.txt").chmod(0o600)
                print(f"{version}: failed at {entry.get('active_stage', 'fixture')} ({type(error).__name__}).", flush=True)
            finally:
                fixture.compose("stop", service)
                fixture.compose("rm", "-f", service)
                entry["container_cleanup"] = "stopped_and_removed_volumes_retained"
                output.write_text(json.dumps(record, indent=2) + "\n")
    finally:
        record["status"] = "passed" if len(record["versions"]) == len(versions) and all(v["status"] == "passed" for v in record["versions"]) else "failed"
        record["finished_utc"] = datetime.now(timezone.utc).isoformat()
        output.write_text(json.dumps(record, indent=2) + "\n")
    if failed:
        raise SystemExit("P7 regression has failures; inspect the scoped local records.")


if __name__ == "__main__":
    main()
