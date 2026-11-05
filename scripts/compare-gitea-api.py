#!/usr/bin/env python3
"""Compare the seven pinned Gitea Swagger 2.0 specifications as Markdown."""

import argparse
import hashlib
import json
from pathlib import Path

VERSIONS = ("1.22.6", "1.23.8", "1.24.7", "1.25.5", "1.26.4", "1.27.3", "28.3.0")
METHODS = {"get", "put", "post", "delete", "patch", "head", "options"}
SHAPE_KEYS = (
    "$ref", "type", "format", "items", "additionalProperties", "enum", "default",
    "minimum", "maximum", "minLength", "maxLength", "minItems", "maxItems",
    "pattern", "uniqueItems", "allOf", "x-nullable", "readOnly", "collectionFormat",
    "multipleOf", "exclusiveMinimum", "exclusiveMaximum", "minProperties", "maxProperties",
)


def shape(value):
    """Keep contract-relevant schema fields; omit prose and generated Go names."""
    if isinstance(value, list):
        return [shape(item) for item in value]
    if not isinstance(value, dict):
        return value
    result = {key: shape(value[key]) for key in SHAPE_KEYS if key in value}
    if "properties" in value:
        result["properties"] = {key: shape(item) for key, item in sorted(value["properties"].items())}
    if "required" in value and isinstance(value["required"], list):
        result["required"] = sorted(value["required"])
    if "enum" in result:
        result["enum"] = sorted(result["enum"], key=str)
    return result


def operations(spec):
    return {
        (method.upper(), path): (spec, path_item, operation)
        for path, path_item in spec["paths"].items()
        for method, operation in path_item.items()
        if method.lower() in METHODS
    }


def contract(spec, path_item, operation):
    params = {}
    for item in path_item.get("parameters", []) + operation.get("parameters", []):
        if "$ref" in item:
            name = ("$ref", item["$ref"])
        else:
            name = (item.get("in", ""), item.get("name", ""))
        params[name] = {
            "required": item.get("required", False),
            "shape": shape(item),
            "schema": shape(item.get("schema", {})),
        }
    responses = {
        code: {
            "ref": body.get("$ref"),
            "schema": shape(body.get("schema", {})),
        }
        for code, body in operation.get("responses", {}).items()
    }
    return {
        "params": params,
        "responses": responses,
        "consumes": sorted(operation.get("consumes", spec.get("consumes", []))),
        "produces": sorted(operation.get("produces", spec.get("produces", []))),
        "security": operation.get("security", spec.get("security")),
    }


def compact(value):
    return json.dumps(value, ensure_ascii=False, sort_keys=True, separators=(",", ":"))


def inline(value):
    return "\u0060" + str(value).replace("\u0060", "\u0027") + "\u0060"


def report(before, after, a, b):
    left, right = operations(before), operations(after)
    print(f"## {a} → {b}\n")
    added, removed = sorted(right.keys() - left.keys()), sorted(left.keys() - right.keys())
    print(f"新增 {len(added)} 项，移除 {len(removed)} 项；按 HTTP 方法和路径的精确字符串比较。\n")
    for title, keys in (("新增操作", added), ("移除操作", removed)):
        print(f"### {title}\n")
        if not keys:
            print("无。\n")
        for method, path in keys:
            print(f"- {inline(method + ' ' + path)}")
        if keys:
            print()

    print("### 已有操作的契约变更\n")
    count = 0
    for key in sorted(left.keys() & right.keys()):
        old, new = contract(*left[key]), contract(*right[key])
        parts = []
        for k in ("params", "responses"):
            before_map, after_map = old[k], new[k]
            for item in sorted(before_map.keys() | after_map.keys()):
                if before_map.get(item) != after_map.get(item):
                    name = "/".join(item) if isinstance(item, tuple) else item
                    parts.append(f"{k} {name}: {compact(before_map.get(item))} → {compact(after_map.get(item))}")
        for k in ("consumes", "produces", "security"):
            if old[k] != new[k]:
                parts.append(f"{k}: {compact(old[k])} → {compact(new[k])}")
        if parts:
            count += 1
            print(f"- {inline(key[0] + ' ' + key[1])}")
            for part in parts:
                print(f"  - {inline(part)}")
    if not count:
        print("未发现本脚本覆盖的契约变更。")
    print()

    print("### 数据模型变更\n")
    old_defs, new_defs = before.get("definitions", {}), after.get("definitions", {})
    for label, names in (("新增模型", sorted(new_defs.keys() - old_defs.keys())),
                         ("移除模型", sorted(old_defs.keys() - new_defs.keys()))):
        print(f"- {label}：" + ("、".join(inline(name) for name in names) if names else "无") + "。")
    print()
    count = 0
    for name in sorted(old_defs.keys() & new_defs.keys()):
        old, new = old_defs[name], new_defs[name]
        p1, p2 = old.get("properties", {}), new.get("properties", {})
        details = []
        for field in sorted(p1.keys() | p2.keys()):
            a_shape = shape(p1[field]) if field in p1 else None
            b_shape = shape(p2[field]) if field in p2 else None
            if a_shape != b_shape:
                details.append(f"{field}: {compact(a_shape)} → {compact(b_shape)}")
        if sorted(old.get("required", [])) != sorted(new.get("required", [])):
            details.append(f"required: {compact(sorted(old.get('required', [])))} → {compact(sorted(new.get('required', [])))}")
        old_shape = shape({key: value for key, value in old.items() if key not in ("properties", "required")})
        new_shape = shape({key: value for key, value in new.items() if key not in ("properties", "required")})
        if old_shape != new_shape:
            details.append(f"模型本身: {compact(old_shape)} → {compact(new_shape)}")
        if details:
            count += 1
            print(f"- {inline(name)}")
            for detail in details:
                print(f"  - {inline(detail)}")
    if not count:
        print("未发现本脚本覆盖的模型变更。")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("spec_dir", type=Path, help="directory containing gitea-swagger-VERSION.json files")
    args = parser.parse_args()
    specs = {}
    print("# Gitea API 逐版本差异明细\n")
    print("由 scripts/compare-gitea-api.py 生成；解读、来源与局限见 [gitea-api-compatibility.md](gitea-api-compatibility.md)。")
    print("比较契约字段而非说明文字；null 表示该项不存在，不代表 JSON 字段接受 null。\n")
    print("| 版本 | 路径数 | 操作数 | 模型数 | 源文件 SHA-256 |")
    print("| --- | ---: | ---: | ---: | --- |")
    for version in VERSIONS:
        raw = (args.spec_dir / f"gitea-swagger-{version}.json").read_bytes()
        spec = json.loads(raw)
        if spec.get("swagger") != "2.0":
            raise ValueError(f"{version}: expected Swagger 2.0")
        specs[version] = spec
        print(f"| {version} | {len(spec['paths'])} | {len(operations(spec))} | {len(spec.get('definitions', {}))} | {inline(hashlib.sha256(raw).hexdigest())} |")
    print()
    for index, (a, b) in enumerate(zip(VERSIONS, VERSIONS[1:])):
        if index:
            print()
        report(specs[a], specs[b], a, b)


if __name__ == "__main__":
    main()
