package issue

import (
	"context"
	"strconv"

	"github.com/Spring-Ocean/gitea-cli-so/internal/app"
	"github.com/Spring-Ocean/gitea-cli-so/internal/gitea"
)

// ResolveReferences 为 Issue 与 PR 解析仓库标签和里程碑，保留明确清空指针。
// 多个来源互斥，名称或 ID 必须唯一；仅查询目录，不向现有 Issue 或 PR 写入。
func ResolveReferences(ctx context.Context, client *gitea.Client, base string, changes Changes, labels []string, milestone string) (Changes, error) {
	changes = copyChanges(changes)
	if (len(labels) > 0 && changes.Labels != nil) || (milestone != "" && changes.Milestone != nil) {
		return Changes{}, app.Wrap(app.InvalidArgument, "Provide one source for each label or milestone change.", nil)
	}
	references := append([]string{}, labels...)
	if changes.Labels != nil {
		for _, id := range *changes.Labels {
			if id < 1 {
				return Changes{}, app.Wrap(app.InvalidArgument, "Label IDs must be positive.", nil)
			}
			references = append(references, strconv.FormatInt(id, 10))
		}
	}
	if len(references) > 0 {
		values, err := resolveLabels(ctx, client, base, references)
		if err != nil {
			return Changes{}, err
		}
		ids := []int64{}
		for _, value := range values {
			ids = append(ids, value.ID)
		}
		changes.Labels = &ids
	}
	if changes.Milestone != nil && *changes.Milestone < 0 {
		return Changes{}, app.Wrap(app.InvalidArgument, "Milestone must be positive, or 0 for an explicit clear.", nil)
	}
	if milestone == "" && changes.Milestone != nil && *changes.Milestone > 0 {
		milestone = strconv.FormatInt(*changes.Milestone, 10)
	}
	if milestone != "" {
		id, err := resolveMilestone(ctx, client, base, milestone)
		if err != nil {
			return Changes{}, err
		}
		changes.Milestone = &id
	}
	return changes, nil
}

// MatchChanges 核对 Issue 或 PR 的明确共同字段，未提供字段不参与比较。
// 集合按稳定 ID／账号比较，允许调用者在独立步骤处理标签。
func MatchChanges(value Issue, changes Changes, labels bool) bool {
	return changesMatch(value, changes, labels)
}
