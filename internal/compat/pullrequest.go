package compat

// MergeFields 按七个精确版本的定义构造合并字段，不猜测未研究版本。
// head_commit_id 始终绑定已确认 SHA，不发送 force 或自动合并，也不换字段重试。
func (info Info) MergeFields(method, head string, deleteBranch bool) map[string]any {
	action := "do"
	switch info.Raw {
	case "1.22.6", "1.23.8", "1.24.7", "1.25.5":
		action = "Do"
	}
	return map[string]any{
		action:                      method,
		"head_commit_id":            head,
		"delete_branch_after_merge": deleteBranch,
	}
}

// CreateReviewers 判断精确版本是否支持在创建 PR 时原生设置审查者。
// 1.22.6 必须先创建再请求审查，不通过忽略字段或重建 PR 降级。
func (info Info) CreateReviewers() bool {
	return info.ExactMatch && info.Raw != "1.22.6"
}
