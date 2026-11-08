package cli

import (
	"github.com/Spring-Ocean/gitea-cli-so/internal/app"
	"github.com/Spring-Ocean/gitea-cli-so/internal/output"
	"github.com/Spring-Ocean/gitea-cli-so/internal/tui"
	"github.com/spf13/cobra"
)

// failureNotice 将错误类别映射为清晰的标题、原因和恢复动作。
// 保留安全错误消息与代码，不依赖错误字符串判断，也不自动重试可能已经写入的请求。
func failureNotice(failure *app.Error) tui.NoticeOptions {
	notice := tui.NoticeOptions{
		Title:   "Unable to complete this action",
		Message: failure.Message,
		Code:    string(failure.Code),
		Kind:    output.StyleError,
		Hint:    "Return to the previous page and review the selected account, repository and options.",
		Choices: []tui.Choice{{ID: "back", Label: "Back", Description: "Return without repeating the action"}},
	}
	switch failure.Code {
	case app.AuthRequired:
		notice.Title = "Connect your account"
		notice.Hint = "The guided setup can verify your identity and save its connection to this Gitea instance."
		notice.Choices = append([]tui.Choice{{
			ID: "connect", Label: "Connect account", Description: "Open the guided account setup",
		}}, notice.Choices...)
	case app.IdentityMismatch:
		notice.Title = "Account does not match"
		notice.Hint = "Review the credential source and choose the account that owns it. No business request was sent with a mismatched identity."
	case app.PermissionDenied:
		notice.Title = "Access was denied"
		notice.Hint = "Check the selected account and its repository permissions. Signing in does not grant additional access."
	case app.NetworkError:
		notice.Title = "Could not reach Gitea"
		notice.Hint = "Check the instance address and network connection. A failed connection does not mean the server is not Gitea."
	case app.TLSError:
		notice.Title = "Could not verify the connection"
		notice.Hint = "Check the instance certificate or configured private CA before trying again."
	case app.ContextRequired, app.ContextAmbiguous:
		notice.Title = "Choose a repository context"
		notice.Hint = "Use Context on Home to select an instance and repository, or browse the repositories linked to your account."
	case app.ConfigError:
		notice.Title = "Review the configuration"
		notice.Hint = "Review the reported file and setting. Correct the configuration or choose another supported configuration path before continuing."
	case app.ConfigConflict, app.Conflict, app.PreconditionChanged:
		notice.Title = "The selected context changed"
		notice.Hint = "Return and refresh the selection before continuing. The action will not be repeated automatically."
	case app.NotFound:
		notice.Title = "The selected item is unavailable"
		notice.Hint = "Refresh the list and check your access. The item may have moved or been removed."
	case app.InvalidArgument:
		notice.Title = "Review your input"
		notice.Hint = "Go back to the form and correct the highlighted operation's inputs."
	case app.ResultUnknown, app.PartialFailure:
		notice.Title = "Check the operation outcome"
		notice.Kind = output.StyleWarning
		notice.Hint = "Some changes may already exist on the server. Check the saved result and remote state before making another write."
	}
	return notice
}

// showMenuFailure 以结构化状态卡呈现错误，并允许用户主动进入账号引导。
// 写操作从不由恢复按钮重新提交；浏览工作台中的身份失效只能回到原导航后重新连接。
func (r *runtime) showMenuFailure(command *cobra.Command, failure *app.Error) error {
	notice := failureNotice(failure)
	if r.options.browseScope != nil && failure.Code == app.AuthRequired {
		notice.Choices = []tui.Choice{{ID: "back", Label: "Back", Description: "Return to Home to reconnect"}}
		notice.Hint = "Return to Home and reconnect this account, then open Browse account again."
	}
	action, err := r.session.Notice(command.Context(), notice)
	if err != nil {
		if app.Failure(err).Code == app.Cancelled && command.Context().Err() == nil {
			return nil
		}
		return err
	}
	if action == "connect" {
		err = r.connectAccount(command, false)
		if err != nil && app.Failure(err).Code == app.Cancelled && command.Context().Err() == nil {
			return nil
		}
		return err
	}
	return nil
}
