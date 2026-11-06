#!/usr/bin/env python3
"""提供只用于隔离测试的 Git credential helper。

返回固定的合成身份；日志仅保留上下文及敏感字段是否存在。
"""

import json
import os
from pathlib import Path
import sys
import time


def main():
    """按 Git helper 协议处理 get、store 和 erase。

    日志路径由调用者提供，凭证值不会写入日志或普通诊断。
    """
    operation = sys.argv[1] if len(sys.argv) > 1 else ""
    fields = {}
    for line in sys.stdin:
        line = line.rstrip("\n")
        if not line:
            break
        key, _, value = line.partition("=")
        fields[key] = value
    event = {key: fields[key] for key in ("protocol", "host", "path", "username") if key in fields}
    event["operation"] = operation
    event["password_present"] = "password" in fields
    if os.environ.get("SOTEA_TEST_HELPER_LOG"):
        log = Path(os.environ["SOTEA_TEST_HELPER_LOG"])
        with log.open("a", encoding="utf-8") as stream:
            stream.write(json.dumps(event) + "\n")
    time.sleep(float(os.environ.get("SOTEA_TEST_HELPER_DELAY", "0")))
    if operation == "get" and not os.environ.get("SOTEA_TEST_HELPER_EMPTY"):
        print("username=" + os.environ.get("SOTEA_TEST_HELPER_USERNAME", "p0-owner"))
        print("password=sotea-synthetic-helper-password")
        print()


if __name__ == "__main__":
    main()
