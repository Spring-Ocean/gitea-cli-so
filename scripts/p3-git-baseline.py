#!/usr/bin/env python3
"""验证显式隔离 Git 样本的 sotea 内核参数、helper 和透传契约。

不替换系统 Git，不安装到全局目录，不访问真实凭证。
"""

import argparse
from datetime import datetime, timezone
import hashlib
import json
import os
from pathlib import Path
import subprocess

ROOT = Path(__file__).resolve().parents[1]


def main():
    """将指定 Git 目录仅加入测试子进程 PATH，并记录真实版本与摘要。

    GIT_EXEC_PATH 同样仅影响隔离子进程；样本构建选项另在证据中明确。
    """
    parser = argparse.ArgumentParser(description="Verify an isolated Git baseline without changing system Git.")
    parser.add_argument("--git", type=Path, required=True)
    args = parser.parse_args()
    binary = args.git.resolve()
    environment = dict(os.environ, PATH=str(binary.parent) + os.pathsep + os.environ.get("PATH", ""),
                       GIT_EXEC_PATH=str(binary.parent), GIT_CONFIG_NOSYSTEM="1", GIT_CONFIG_GLOBAL=os.devnull)
    version = subprocess.check_output([str(binary), "--version"], env=environment, text=True).strip()
    result = subprocess.run(["go", "test", "./internal/git", "./internal/context", "./internal/cli", "-count=1"],
                            cwd=ROOT, env=environment, capture_output=True, text=True, timeout=120)
    if result.returncode:
        print(result.stdout)
        print(result.stderr)
        raise SystemExit("Isolated Git baseline validation failed.")
    record = {"schema_version": 1, "time_utc": datetime.now(timezone.utc).isoformat(), "status": "passed",
              "version": version, "sha256": hashlib.sha256(binary.read_bytes()).hexdigest(),
              "packages": ["internal/git", "internal/context", "internal/cli"],
              "scope": "core Git, repository/worktree, isolated helper, and passthrough; not a distributed Git installation",
              "sample": "Official git-2.43.7 archive; temporary local build without curl/expat/gettext/tcltk/OpenSSL"}
    (ROOT / "tests/integration/.state/p3-git-baseline.json").write_text(json.dumps(record, indent=2) + "\n")
    print(version + ": isolated core/helper/context/CLI checks passed.")


if __name__ == "__main__":
    main()
