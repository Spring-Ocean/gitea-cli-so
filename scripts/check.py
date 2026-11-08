#!/usr/bin/env python3
"""运行指定工具链的阶段检查并构建候选平台产物。

构建记录位于受忽略的 dist/<phase>；交叉编译不等于目标系统运行验收。
"""

import argparse
from datetime import datetime, timezone
import hashlib
import json
import os
from pathlib import Path
import subprocess
import sys

ROOT = Path(__file__).resolve().parents[1]


def run(args, environment):
    """以参数数组执行检查或构建，并明确传播失败。

    不经过 shell，不修改宿主的 HOME 或用户配置。
    """
    subprocess.run(args, cwd=ROOT, env=environment, check=True)


def main():
    """默认执行测试、静态检查和三个候选平台构建。

    build-only 仅构建并在记录中标明检查未执行，不复用过时测试结论。
    """
    parser = argparse.ArgumentParser(description="Check and build sotea development artifacts.")
    parser.add_argument("--build-only", action="store_true", help="Build without rerunning tests or vet")
    parser.add_argument("--phase", choices=("p1", "p2", "p3", "p4", "p5", "p6", "p6-integrations", "p6-workspace", "p7"), default="p1", help="Choose the development artifact directory")
    args = parser.parse_args()
    output = ROOT / "dist" / args.phase
    environment = dict(os.environ, GOTOOLCHAIN="go1.27.3")
    version = subprocess.check_output(["go", "env", "GOVERSION"], cwd=ROOT, env=environment, text=True).strip()
    if version != "go1.27.3":
        raise SystemExit("Go 1.27.3 is required for development builds.")
    checks = {"go_test": "not_run", "go_vet": "not_run"}
    if not args.build_only:
        run(["go", "test", "./..."], environment)
        checks["go_test"] = "passed"
        run(["go", "vet", "./..."], environment)
        checks["go_vet"] = "passed"
    revision = subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=ROOT, text=True).strip()
    dirty = bool(subprocess.check_output(["git", "status", "--porcelain"], cwd=ROOT, text=True).strip())
    build_version = f"p7-{revision[:7]}" if args.phase == "p7" else f"dev-{revision[:7]}"
    build_options = ["-trimpath"]
    if args.phase == "p7":
        build_options.append("-buildvcs=false")
    artifacts = []
    for system, arch in (("darwin", "arm64"), ("darwin", "amd64"), ("windows", "amd64")):
        directory = output / f"{system}-{arch}"
        directory.mkdir(parents=True, exist_ok=True)
        target = directory / ("sotea.exe" if system == "windows" else "sotea")
        candidate_env = dict(environment, GOOS=system, GOARCH=arch, CGO_ENABLED="0")
        run(["go", "build", *build_options, "-ldflags", f"-X main.version={build_version}",
             "-o", str(target), "./cmd/sotea"], candidate_env)
        artifacts.append({"os": system, "arch": arch, "path": str(target.relative_to(ROOT)),
                          "sha256": hashlib.sha256(target.read_bytes()).hexdigest(), "runtime_status": "not_tested"})
        print(f"Built {system}/{arch}.", flush=True)
    sources = [ROOT / "go.mod", ROOT / "go.sum", *(ROOT / "cmd").rglob("*.go"), *(ROOT / "internal").rglob("*.go")]
    record = {"schema_version": 1, "time_utc": datetime.now(timezone.utc).isoformat(), "go": version,
              "commit": revision, "version": build_version, "build_options": [*build_options, "CGO_ENABLED=0"], "worktree_dirty": dirty, "checks": checks, "artifacts": artifacts,
              "source_sha256": {str(p.relative_to(ROOT)): hashlib.sha256(p.read_bytes()).hexdigest() for p in sorted(sources)}}
    output.mkdir(parents=True, exist_ok=True)
    (output / "build-info.json").write_text(json.dumps(record, indent=2) + "\n")
    print(f"Build metadata: dist/{args.phase}/build-info.json. Target-system runtime checks are still required.")


if __name__ == "__main__":
    try:
        main()
    except subprocess.CalledProcessError:
        sys.exit("A required check or build failed.")
