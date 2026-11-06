#!/usr/bin/env python3
"""为 P2 的固定 loopback 实例返回隔离测试账号凭证。

密钥只从本次脚本进程环境取得，不写日志，不查询系统真实 helper。
"""

import json
import os
from pathlib import Path
import sys


def main():
    """仅允许向 P0 固定端口和测试用户名返回合成凭证。

    日志只保存非敏感匹配字段，store/erase 不持久化密钥。
    """
    operation = sys.argv[1] if len(sys.argv) > 1 else ""
    fields = {}
    for line in sys.stdin:
        line = line.rstrip("\n")
        if not line:
            break
        key, _, value = line.partition("=")
        fields[key] = value
    if fields.get("protocol") != "http" or fields.get("host") not in {
        "127.0.0.1:" + str(port) for port in (32206, 32308, 32407, 32505, 32604, 32703, 32830)
    }:
        return
    username = fields.get("username") or os.environ.get("SOTEA_P2_HELPER_USER", "p0-owner")
    if username not in ("p0-owner", "p0-reviewer", "p0-limited"):
        return
    if os.environ.get("SOTEA_P2_HELPER_LOG"):
        event = {key: fields.get(key, "") for key in ("protocol", "host", "path")}
        event.update(operation=operation, username=username)
        with Path(os.environ["SOTEA_P2_HELPER_LOG"]).open("a", encoding="utf-8") as stream:
            stream.write(json.dumps(event) + "\n")
    if operation == "get":
        credentials = json.loads(os.environ["SOTEA_P2_CREDENTIALS"])
        print("username=" + username)
        print("password=" + credentials[username])
        print()


if __name__ == "__main__":
    main()
