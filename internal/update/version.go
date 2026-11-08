// Package update 从固定 GitHub 仓库检查版本并安装经过校验的平台产物。
// 本包不访问 Gitea 凭证或终端，安装必须由入口在用户授权后调用。
package update

import (
	"strconv"
	"strings"
)

// version 保存三段数字与预发布标识，不以构建元数据决定更新顺序。
// 仅接受完整语义版本，避免把任意远端标签当成可安装版本。
type version struct {
	numbers [3]uint64
	pre     []string
}

// parseVersion 校验发布标签，允许 v 前缀和语义版本构建元数据。
// 数字前导零、空标识及非法字符均返回失败，内部开发标记不参与自动更新。
func parseVersion(value string) (version, bool) {
	value = strings.TrimPrefix(value, "v")
	base, metadata, hasMetadata := strings.Cut(value, "+")
	if hasMetadata && !validIdentifiers(metadata, false) {
		return version{}, false
	}
	core, prerelease, hasPrerelease := strings.Cut(base, "-")
	parts := strings.Split(core, ".")
	if len(parts) != 3 || (hasPrerelease && !validIdentifiers(prerelease, true)) {
		return version{}, false
	}
	parsed := version{}
	for index, part := range parts {
		if !numeric(part) || (len(part) > 1 && part[0] == '0') {
			return version{}, false
		}
		number, err := strconv.ParseUint(part, 10, 64)
		if err != nil {
			return version{}, false
		}
		parsed.numbers[index] = number
	}
	if hasPrerelease {
		parsed.pre = strings.Split(prerelease, ".")
	}
	return parsed, true
}

// validIdentifiers 检查预发布或构建元数据的点分标识。
// 预发布数字禁止前导零，标识长度不依赖整数解析范围。
func validIdentifiers(value string, prerelease bool) bool {
	for _, part := range strings.Split(value, ".") {
		if part == "" || (prerelease && numeric(part) && len(part) > 1 && part[0] == '0') {
			return false
		}
		for _, char := range part {
			if !(char >= '0' && char <= '9' || char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char == '-') {
				return false
			}
		}
	}
	return true
}

// numeric 识别非空十进制数字串。
// 不做整数转换，以支持语义版本中任意长度的预发布序号。
func numeric(value string) bool {
	if value == "" {
		return false
	}
	for _, char := range value {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}

// compare 比较完整语义版本，负数表示左侧更旧。
// 稳定版高于相同核心版本的预发布版，数字标识低于非数字标识。
func compare(left, right version) int {
	for index := range left.numbers {
		if left.numbers[index] < right.numbers[index] {
			return -1
		}
		if left.numbers[index] > right.numbers[index] {
			return 1
		}
	}
	if len(left.pre) == 0 && len(right.pre) != 0 {
		return 1
	}
	if len(left.pre) != 0 && len(right.pre) == 0 {
		return -1
	}
	for index := 0; index < min(len(left.pre), len(right.pre)); index++ {
		a, b := left.pre[index], right.pre[index]
		if a == b {
			continue
		}
		if numeric(a) && numeric(b) && len(a) != len(b) {
			if len(a) < len(b) {
				return -1
			}
			return 1
		}
		if numeric(a) != numeric(b) {
			if numeric(a) {
				return -1
			}
			return 1
		}
		return strings.Compare(a, b)
	}
	return len(left.pre) - len(right.pre)
}
