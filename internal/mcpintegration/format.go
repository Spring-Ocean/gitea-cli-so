package mcpintegration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/Spring-Ocean/gitea-cli-so/internal/app"
	"github.com/pelletier/go-toml/v2"
)

const blockStart = "# BEGIN sotea managed MCP integration\n"
const blockEnd = "# END sotea managed MCP integration\n"

// document 保存原始文本及唯一条目的字节范围。
// 只修改本条目或托管块，其他字段、注释和排版保持原样。
type document struct {
	client    Client
	raw       []byte
	entry     map[string]any
	start     int
	end       int
	container objectSpan
	root      objectSpan
	contained bool
}

// memberSpan 标识 JSONC 对象成员和相邻分隔符。
// 字节位置基于等长净化文本，可以直接应用到原始含注释文档。
type memberSpan struct {
	key   string
	start int
	value int
	end   int
	comma int
}

// objectSpan 保存对象内部的位置与成员列表。
// 未找到的容器不伪造成空对象，由调用者显式创建。
type objectSpan struct {
	close   int
	members []memberSpan
}

// parseDocument 验证完整文档并定位待管理条目。
// 无效配置、重复目标键或非对象容器均报错，不重置现有文件。
func parseDocument(client Client, raw []byte) (document, error) {
	d := document{client: client, raw: raw, start: -1}
	if client == Codex {
		var config map[string]any
		if err := toml.Unmarshal(raw, &config); err != nil {
			return d, app.Wrap(app.ConfigError, "The Codex configuration contains invalid TOML. Fix it before installing sotea.", nil)
		}
		if value, exists := config["mcp_servers"]; exists {
			servers, ok := value.(map[string]any)
			if !ok {
				return d, app.Wrap(app.ConfigError, "mcp_servers must be a table.", nil)
			}
			if entry, exists := servers["sotea"]; exists {
				d.entry, ok = entry.(map[string]any)
				if !ok {
					return d, app.Wrap(app.ConfigError, "The existing sotea entry is not a table.", nil)
				}
			}
		}
		if bytes.Count(raw, []byte(blockStart)) == 1 && bytes.Count(raw, []byte(blockEnd)) == 1 {
			d.start = bytes.Index(raw, []byte(blockStart))
			d.end = bytes.Index(raw, []byte(blockEnd)) + len(blockEnd)
			if d.end <= d.start {
				d.start = -1
			} else {
				// 标记之外追加的 TOML 键仍可能属于同一张表，必须同时核对语义条目。
				var managed struct {
					Servers map[string]map[string]any `toml:"mcp_servers"`
				}
				if err := toml.Unmarshal(raw[d.start:d.end], &managed); err != nil {
					d.start = -1
				} else {
					blockEntry, blockErr := json.Marshal(managed.Servers["sotea"])
					fullEntry, fullErr := json.Marshal(d.entry)
					if blockErr != nil || fullErr != nil || !bytes.Equal(blockEntry, fullEntry) {
						d.start = -1
					}
				}
			}
		}
		return d, nil
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		raw = []byte("{}\n")
		d.raw = raw
	}
	clean, err := cleanJSONC(raw)
	if err != nil || !json.Valid(clean) {
		return d, app.Wrap(app.ConfigError, "The client configuration contains invalid JSON or JSONC. Fix it before installing sotea.", nil)
	}
	d.root, err = parseObject(clean, skipSpace(clean, 0))
	if err != nil {
		return d, err
	}
	for _, member := range d.root.members {
		if member.key != clientRoot(client) {
			continue
		}
		d.contained = true
		d.container, err = parseObject(clean, member.value)
		if err != nil {
			return d, err
		}
		for _, server := range d.container.members {
			if server.key == "sotea" {
				d.start, d.end = server.value, server.end
				if err := json.Unmarshal(clean[d.start:d.end], &d.entry); err != nil || d.entry == nil {
					return d, app.Wrap(app.ConfigError, "The existing sotea entry must be an object.", nil)
				}
			}
		}
	}
	return d, nil
}

// cleanJSONC 将注释和尾随逗号替换成等长空白，保留字符串字面内容。
// 非闭合注释或字符串报错；位置不变才能安全修改原文本的单个成员。
func cleanJSONC(raw []byte) ([]byte, error) {
	clean := bytes.Clone(raw)
	quoted, escaped := false, false
	for i := 0; i < len(raw); i++ {
		if quoted {
			if escaped {
				escaped = false
			} else if raw[i] == '\\' {
				escaped = true
			} else if raw[i] == '"' {
				quoted = false
			}
			continue
		}
		if raw[i] == '"' {
			quoted = true
			continue
		}
		if raw[i] != '/' || i+1 == len(raw) {
			continue
		}
		start := i
		switch raw[i+1] {
		case '/':
			for i < len(raw) && raw[i] != '\n' {
				i++
			}
		case '*':
			end := bytes.Index(raw[i+2:], []byte("*/"))
			if end < 0 {
				return nil, fmt.Errorf("Unterminated JSONC comment")
			}
			i += end + 4
		default:
			continue
		}
		for j := start; j < i; j++ {
			if clean[j] != '\n' && clean[j] != '\r' {
				clean[j] = ' '
			}
		}
		i--
	}
	quoted, escaped = false, false
	for i, value := range clean {
		if quoted {
			if escaped {
				escaped = false
			} else if value == '\\' {
				escaped = true
			} else if value == '"' {
				quoted = false
			}
			continue
		}
		if value == '"' {
			quoted = true
		} else if value == ',' {
			next := skipSpace(clean, i+1)
			if next < len(clean) && (clean[next] == '}' || clean[next] == ']') {
				clean[i] = ' '
			}
		}
	}
	return clean, nil
}

// skipSpace 返回当前位置之后第一个非 JSON 空白字符的位置。
// 输入已净化为等长 JSON，不能用 Unicode trim 改变原始字节偏移。
func skipSpace(raw []byte, position int) int {
	for position < len(raw) && strings.ContainsRune(" \t\r\n", rune(raw[position])) {
		position++
	}
	return position
}

// parseObject 读取一个对象的直接成员范围并拒绝重复键。
// 值由标准 JSON 解码器跳过，嵌套字符串中的逗号或大括号不会影响定位。
func parseObject(clean []byte, start int) (objectSpan, error) {
	object := objectSpan{}
	if start >= len(clean) || clean[start] != '{' {
		return object, app.Wrap(app.ConfigError, "The configuration and server container must be JSON objects.", nil)
	}
	seen := map[string]bool{}
	position := skipSpace(clean, start+1)
	for position < len(clean) && clean[position] != '}' {
		member := memberSpan{start: position, comma: -1}
		decoder := json.NewDecoder(bytes.NewReader(clean[position:]))
		if err := decoder.Decode(&member.key); err != nil || seen[member.key] {
			return object, app.Wrap(app.ConfigError, "The client configuration contains duplicate or invalid keys.", nil)
		}
		seen[member.key] = true
		position = skipSpace(clean, position+int(decoder.InputOffset()))
		if position >= len(clean) || clean[position] != ':' {
			return object, app.Wrap(app.ConfigError, "Invalid configuration member.", nil)
		}
		member.value = skipSpace(clean, position+1)
		decoder = json.NewDecoder(bytes.NewReader(clean[member.value:]))
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return object, app.Wrap(app.ConfigError, "Invalid configuration value.", nil)
		}
		member.end = member.value + int(decoder.InputOffset())
		position = skipSpace(clean, member.end)
		if position < len(clean) && clean[position] == ',' {
			member.comma = position
			position = skipSpace(clean, position+1)
		}
		object.members = append(object.members, member)
	}
	if position >= len(clean) {
		return object, io.ErrUnexpectedEOF
	}
	object.close = position
	return object, nil
}

// entryBytes 返回用于托管识别的原始条目或 Codex 标记块。
// 未托管 TOML 即使有同名表也不产生有效范围，不能被自动接管。
func (d document) entryBytes() []byte {
	if d.entry == nil || d.start < 0 {
		return nil
	}
	return d.raw[d.start:d.end]
}

// change 在内存生成替换结果并重新验证完整文档。
// remove 仅删除 sotea 条目；不删除空的服务器容器或客户端配置文件。
func (d document) change(entry map[string]any, remove bool) ([]byte, error) {
	encoded, err := encodeEntry(d.client, entry)
	if err != nil {
		return nil, err
	}
	var next []byte
	if d.client == Codex {
		block := append([]byte(blockStart), encoded...)
		block = append(block, blockEnd...)
		if remove {
			block = nil
		}
		if d.entry != nil {
			if d.start < 0 {
				return nil, app.Wrap(app.ConfigConflict, "The Codex sotea entry is not in a managed block.", nil)
			}
			next = splice(d.raw, d.start, d.end, block)
		} else {
			next = append(bytes.Clone(d.raw), '\n')
			next = append(next, block...)
		}
	} else if d.entry != nil {
		if remove {
			for i, member := range d.container.members {
				if member.key != "sotea" {
					continue
				}
				start, end := member.start, member.end
				trailing, _ := cleanJSONC(d.raw[member.end:d.container.close])
				if comma := bytes.IndexByte(trailing, ','); comma >= 0 {
					end = member.end + comma + 1
				}
				if member.comma >= 0 {
					end = member.comma + 1
				} else if i > 0 && d.container.members[i-1].comma >= 0 {
					start = d.container.members[i-1].comma
				}
				next = splice(d.raw, start, end, nil)
			}
		} else {
			next = splice(d.raw, d.start, d.end, encoded)
		}
	} else if d.contained {
		next = d.insert(d.container, "sotea", encoded)
	} else {
		value := append([]byte("{\n    \"sotea\": "), encoded...)
		value = append(value, []byte("\n  }")...)
		next = d.insert(d.root, clientRoot(d.client), value)
	}
	if _, err := parseDocument(d.client, next); err != nil {
		return nil, app.Wrap(app.ConfigConflict, "The configuration layout cannot be safely extended. Use a standard server table or another configuration path.", nil)
	}
	return next, nil
}

// insert 将新成员插入对象尾部，兼容已有注释和 JSONC 尾随逗号。
// 不重排原成员或重写其他服务器内容；插入后由完整解码验证语法。
func (d document) insert(object objectSpan, key string, value []byte) []byte {
	prefix := ""
	if len(object.members) > 0 {
		last := object.members[len(object.members)-1]
		// cleanJSONC 已移除的尾随逗号需要在原文中识别，避免生成双逗号。
		between, _ := cleanJSONC(d.raw[last.end:object.close])
		if !bytes.Contains(between, []byte(",")) {
			prefix = ","
		}
	}
	addition := []byte(prefix + "\n  \"" + key + "\": ")
	addition = append(addition, value...)
	addition = append(addition, '\n')
	return splice(d.raw, object.close, object.close, addition)
}

// splice 在独立缓冲中执行一次精确字节替换。
// 输入快照保持不可变，以便安装前比较和失败恢复使用同一内容。
func splice(raw []byte, start, end int, replacement []byte) []byte {
	next := append([]byte{}, raw[:start]...)
	next = append(next, replacement...)
	return append(next, raw[end:]...)
}
