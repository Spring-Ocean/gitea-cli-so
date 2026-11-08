package mcp

// schema 是协议注册所需的封闭 JSON Schema，不泄露内部请求模型。
// 分支由 SDK 实际校验，不能用任意 map 输入绕过 action 与 params 的对应关系。
type schema map[string]any

// objectSchema 构造封闭对象，必填列表只包含实际需要的字段。
// 远端数据和受校验 API 名称映射单独声明扩展能力。
func objectSchema(properties schema, required ...string) schema {
	result := schema{"type": "object", "properties": properties, "additionalProperties": false}
	if len(required) > 0 {
		result["required"] = required
	}
	return result
}

// textSchema 定义字符串字段，不以默认空值代替缺失。
// minLength 仅用于确实要求非空的字段，域语义还由共享服务核验。
func textSchema(nonempty bool) schema {
	result := schema{"type": "string"}
	if nonempty {
		result["minLength"] = 1
	}
	return result
}

// integerSchema 限制正编号与明确允许的零值。
// JSON Schema 整数校验后仍用 int64 解码，超范围输入不会被浮点截断。
func integerSchema(minimum int64) schema {
	return schema{"type": "integer", "minimum": minimum, "maximum": int64(1<<53 - 1)}
}

// listSchema 构造有界数组，保留显式空集合的语义。
// 目标批量和集合字段均有独立上限，防止超大参数进入应用服务。
func listSchema(item schema, minimum int) schema {
	return schema{"type": "array", "items": item, "minItems": minimum, "maxItems": 100}
}

// enumSchema 限定稳定字符串枚举，不接受未实现动作。
// 默认值由具体入口设置，不能在此对可选字段隐式补值。
func enumSchema(values ...string) schema {
	return schema{"type": "string", "enum": values}
}

// contextProperties 提供统一远端上下文及可选本地目录字段。
// 启动授权根由 Host 独立限制，Schema 中出现 workdir 不意味着扩大权限。
func contextProperties(local bool) schema {
	properties := schema{
		"instance": textSchema(true),
		"account":  textSchema(true),
		"repo":     textSchema(true),
	}
	if local {
		properties["workdir"] = textSchema(true)
	}
	return properties
}

// readSchema 为一个固定工具加入其实际支持的字段。
// 不提供 all 参数；分页始终有界，未知字段由 SDK 拒绝。
func readSchema(name string) schema {
	fields := contextProperties(name == "context_get" || name == "git_context")
	switch name {
	case "instance_info":
		fields = schema{"instance": textSchema(true), "account": textSchema(true), "refresh": schema{"type": "boolean"}}
	case "auth_status":
		fields = schema{"instance": textSchema(true), "account": textSchema(true), "check": schema{"type": "boolean"}}
	case "account_repo_list", "account_org_list", "org_repo_list":
		delete(fields, "repo")
	case "git_context":
		fields = schema{"workdir": textSchema(true)}
	}
	required := []string{}
	switch name {
	case "repo_view":
		fields["expected_repository_id"] = integerSchema(1)
	case "org_repo_list":
		fields["organization"] = textSchema(true)
		fields["organization_id"] = integerSchema(1)
		required = append(required, "organization", "organization_id")
	case "commit_status":
		fields["ref"] = textSchema(true)
	case "issue_list":
		fields["state"] = enumSchema("open", "closed", "all")
		fields["q"] = textSchema(false)
		fields["labels"] = listSchema(textSchema(true), 0)
		fields["assignee"] = textSchema(true)
		fields["milestone"] = textSchema(true)
	case "pr_list":
		fields["state"] = enumSchema("open", "closed", "all")
		fields["q"] = textSchema(false)
		fields["base"] = textSchema(true)
		fields["author"] = textSchema(true)
	case "release_list":
		fields["q"] = textSchema(false)
	}
	switch name {
	case "issue_view", "issue_label_list", "issue_comment_list", "pr_view", "pr_files", "pr_commits", "pr_comment_list", "pr_review_list":
		fields["index"] = integerSchema(1)
		required = append(required, "index")
	case "release_view", "release_asset_list":
		fields["tag"] = textSchema(true)
		required = append(required, "tag")
	}
	switch name {
	case "context_get", "instance_info", "auth_status", "repo_view", "issue_view", "pr_view", "release_view", "commit_status", "git_context", "issue_label_list":
	default:
		fields["page"] = integerSchema(1)
		fields["limit"] = schema{"type": "integer", "minimum": 1, "maximum": 100}
	}
	return objectSchema(fields, required...)
}

// changesSchema 固定编辑字段，明确 null 不等于清空。
// PR 允许 base，Release 采用独立字段，至少一个字段由 minProperties 保证。
func changesSchema(kind string) schema {
	fields := schema{
		"title":        textSchema(true),
		"body":         textSchema(false),
		"assignees":    listSchema(textSchema(true), 0),
		"label_ids":    listSchema(integerSchema(1), 0),
		"milestone_id": integerSchema(0),
	}
	if kind == "pr" {
		fields["base"] = textSchema(true)
	}
	if kind == "release" {
		fields = schema{
			"name":       textSchema(true),
			"body":       textSchema(true),
			"target":     textSchema(true),
			"draft":      schema{"type": "boolean"},
			"prerelease": schema{"type": "boolean"},
		}
	}
	result := objectSchema(fields)
	result["minProperties"] = 1
	return result
}

// actionSchema 返回唯一动作允许的公开输入，拒绝其他动作夹带的字段。
// 内部 Request 的字段名由适配层显式转换，不直接作为协议模型。
func actionSchema(action string) schema {
	fields := schema{}
	required := []string{}
	switch action {
	case "issue.create", "pr.create":
		fields = changesSchema("issue")["properties"].(schema)
		required = append(required, "title")
		if action == "pr.create" {
			fields["base"] = textSchema(true)
			fields["head"] = textSchema(true)
			fields["users"] = listSchema(textSchema(true), 0)
			fields["teams"] = listSchema(textSchema(true), 0)
			required = append(required, "base", "head")
		}
	case "issue.edit", "pr.edit", "release.edit":
		kind := "issue"
		if action == "pr.edit" {
			kind = "pr"
		}
		if action == "release.edit" {
			kind = "release"
		}
		fields["changes"] = changesSchema(kind)
		required = append(required, "changes")
	case "issue.close", "issue.reopen", "pr.close", "pr.reopen":
		fields["indices"] = listSchema(integerSchema(1), 1)
		required = append(required, "indices")
	case "issue.comment.add", "pr.comment.add":
		fields["body"] = textSchema(true)
		required = append(required, "body")
	case "issue.assignee.add", "issue.assignee.remove":
		fields["users"] = listSchema(textSchema(true), 1)
		required = append(required, "users")
	case "issue.label.add", "issue.label.remove":
		fields["label_ids"] = listSchema(integerSchema(1), 1)
		required = append(required, "label_ids")
	case "pr.reviewer.add":
		fields["users"] = listSchema(textSchema(true), 1)
		fields["teams"] = listSchema(textSchema(true), 1)
	case "pr.review.create":
		fields["event"] = enumSchema("approve", "comment", "request-changes")
		fields["body"] = textSchema(false)
		fields["commit_id"] = textSchema(true)
		fields["review_id"] = integerSchema(1)
		required = append(required, "event")
	case "pr.merge":
		fields["method"] = enumSchema("merge", "squash", "rebase", "rebase-merge", "fast-forward-only")
		fields["delete_branch"] = schema{"type": "boolean"}
		required = append(required, "method")
	case "release.create":
		fields = schema{
			"tag": textSchema(true), "name": textSchema(false), "body": textSchema(false),
			"target": textSchema(true), "draft": schema{"type": "boolean"}, "prerelease": schema{"type": "boolean"},
		}
		required = append(required, "tag")
	case "release.asset.upload":
		fields["file_path"] = textSchema(true)
		fields["name"] = textSchema(true)
		required = append(required, "file_path")
	case "api.request":
		return apiSchema(false)
	}
	switch action {
	case "issue.create", "pr.create", "issue.close", "issue.reopen", "pr.close", "pr.reopen", "release.create":
	case "release.edit", "release.asset.upload":
		fields["tag"] = textSchema(true)
		required = append(required, "tag")
	default:
		fields["index"] = integerSchema(1)
		required = append(required, "index")
	}
	result := objectSchema(fields, required...)
	if action == "pr.reviewer.add" {
		result["anyOf"] = []schema{{"required": []string{"users"}}, {"required": []string{"teams"}}}
	}
	return result
}

// prepareSchema 构造 action 与 params 逐一对应的封闭联合。
// 未允许原始 API 时其分支不出现在发现结果，也不能绕过实际权限检查。
func prepareSchema(raw bool) schema {
	branches := []schema{}
	names := []string{}
	for _, name := range writeActions {
		if name == "api.request" && !raw {
			continue
		}
		names = append(names, name)
		branches = append(branches, schema{"properties": schema{
			"action": schema{"const": name}, "params": actionSchema(name),
		}})
	}
	result := objectSchema(schema{
		"request_id": schema{"type": "string", "minLength": 1, "maxLength": 128, "pattern": "^[^\\x00-\\x1f\\x7f]+$"},
		"action":     enumSchema(names...),
		"context":    objectSchema(contextProperties(true)),
		"params":     schema{"type": "object"},
	}, "request_id", "action", "context", "params")
	result["oneOf"] = branches
	return result
}

// apiSchema 限制原始 API 请求，重复参数通过字符串数组保留。
// 方法和文件来源封闭；路径、认证头、字段互斥再由共享应用入口校验。
func apiSchema(read bool) schema {
	mapping := schema{"type": "object", "additionalProperties": listSchema(textSchema(false), 1), "maxProperties": 100}
	fields := schema{
		"method": enumSchema("POST", "PUT", "PATCH", "DELETE"),
		"path":   textSchema(true), "query": mapping, "headers": mapping,
	}
	if read {
		fields["method"] = enumSchema("GET", "HEAD", "OPTIONS")
		fields["instance"] = textSchema(true)
		fields["account"] = textSchema(true)
	} else {
		fields["body_text"] = textSchema(false)
		fields["body_file"] = textSchema(true)
		fields["multipart"] = objectSchema(schema{
			"fields": listSchema(objectSchema(schema{"name": textSchema(true), "value": textSchema(false)}, "name", "value"), 0),
			"files":  listSchema(objectSchema(schema{"field_name": textSchema(true), "file_path": textSchema(true)}, "field_name", "file_path"), 1),
		}, "files")
	}
	return objectSchema(fields, "method", "path")
}

// errorSchema 固定公开错误字段，不允许底层 cause 或认证数据混入结果。
// null 表示没有错误，HTTP 状态只在服务端确实返回时提供。
func errorSchema() schema {
	result := objectSchema(schema{
		"code": textSchema(true), "message": textSchema(false),
		"http_status": schema{"type": "integer", "minimum": 100, "maximum": 599},
	}, "code", "message")
	result["type"] = []string{"object", "null"}
	return result
}

// resultSchema 约束共同 envelope 和 MCP 元数据的完整固定形状。
// 远端 data 保持扩展能力；错误与计划字段封闭，不能暴露内部对象或额外凭据。
func resultSchema() schema {
	return objectSchema(schema{
		"schema_version": schema{"const": 1},
		"status":         enumSchema("success", "partial", "failed", "cancelled", "unknown"),
		"context":        objectSchema(schema{"instance": textSchema(false), "account": textSchema(false), "repo": textSchema(false)}),
		"items": schema{"type": "array", "items": objectSchema(schema{
			"target": textSchema(false),
			"status": enumSchema("success", "partial", "failed", "cancelled", "unknown", "skipped", "not_executed"),
			"data":   schema{}, "error": errorSchema(),
		}, "target", "status")},
		"error": errorSchema(),
		"meta": objectSchema(schema{
			"plan": objectSchema(schema{
				"request_id": textSchema(true), "plan_id": textSchema(true), "action": enumSchema(writeActions...),
				"plan_state":  enumSchema("preparing", "prepared", "executing", "completed", "expired", "invalidated"),
				"committable": schema{"type": "boolean"}, "created_at": textSchema(true), "expires_at": textSchema(true),
				"confirmation_token": textSchema(true),
			}, "request_id", "action", "plan_state", "committable", "created_at"),
			"execution": objectSchema(schema{"write_attempted": enumSchema("no", "yes", "unknown")}, "write_attempted"),
			"truncation": objectSchema(schema{
				"truncated": schema{"const": true}, "omitted_fields": schema{"type": "array", "items": textSchema(true)},
				"reason": textSchema(true), "next_action": textSchema(true),
			}, "truncated", "omitted_fields", "reason", "next_action"),
			"api": objectSchema(schema{
				"status":  schema{"type": "integer", "minimum": 100, "maximum": 599},
				"headers": schema{"type": "object"}, "body_encoding": enumSchema("json", "utf8", "base64"), "body": schema{},
			}, "status", "body_encoding"),
		}),
	}, "schema_version", "status", "context", "items", "error")
}

// writeActions 是首版已确认的封闭动作集合。
// 新动作必须同步应用适配、输入 Schema、文档及验收，不能仅追加名称。
var writeActions = []string{
	"issue.create", "issue.edit", "issue.close", "issue.reopen", "issue.comment.add",
	"issue.assignee.add", "issue.assignee.remove", "issue.label.add", "issue.label.remove",
	"pr.create", "pr.edit", "pr.close", "pr.reopen", "pr.comment.add", "pr.review.create", "pr.reviewer.add", "pr.merge",
	"release.create", "release.edit", "release.asset.upload", "api.request",
}
