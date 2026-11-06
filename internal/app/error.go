// Package app 定义 CLI、菜单及 MCP 共用的应用契约。
// 此包不读取终端，也不依赖具体协议或外部服务。
package app

import (
	"context"
	"errors"
)

// Code 是跨入口稳定的错误类别。
// 调用者使用类别判断行为，不能依赖面向人的错误文案。
type Code string

// 应用错误类别按配置、认证、外部调用和执行状态划分。
// HTTPStatus 可保留服务端状态，类别不替代实际远端权限检查。
const (
	InvalidArgument      Code = "INVALID_ARGUMENT"
	ConfigError          Code = "CONFIG_ERROR"
	ConfigConflict       Code = "CONFIG_CONFLICT"
	AuthRequired         Code = "AUTH_REQUIRED"
	IdentityMismatch     Code = "AUTH_IDENTITY_MISMATCH"
	PermissionDenied     Code = "PERMISSION_DENIED"
	ContextRequired      Code = "CONTEXT_REQUIRED"
	ContextAmbiguous     Code = "CONTEXT_AMBIGUOUS"
	NotFound             Code = "NOT_FOUND"
	Conflict             Code = "CONFLICT"
	VersionUnknown       Code = "VERSION_UNKNOWN"
	CompatibilityError   Code = "COMPATIBILITY_ERROR"
	PreconditionChanged  Code = "PRECONDITION_CHANGED"
	FilterLimit          Code = "FILTER_LIMIT"
	ResourceTypeMismatch Code = "RESOURCE_TYPE_MISMATCH"
	NetworkError         Code = "NETWORK_ERROR"
	TLSError             Code = "TLS_ERROR"
	ServerError          Code = "SERVER_ERROR"
	IOError              Code = "IO_ERROR"
	GitUnavailable       Code = "GIT_UNAVAILABLE"
	GitError             Code = "GIT_ERROR"
	ResponseTooLarge     Code = "RESPONSE_TOO_LARGE"
	Cancelled            Code = "CANCELLED"
	PartialFailure       Code = "PARTIAL_FAILURE"
	ResultUnknown        Code = "RESULT_UNKNOWN"
)

// Error 保留稳定类别、非敏感提示和底层原因。
// Cause 仅用于 errors.Is／As；输出层不得直接拼接或序列化该字段。
type Error struct {
	Code       Code   `json:"code"`
	Message    string `json:"message"`
	HTTPStatus int    `json:"http_status,omitempty"`
	Cause      error  `json:"-"`
}

// Error 返回面向调用者的安全摘要。
// 不附加 Cause，以免请求地址或 helper 输出中的密钥进入诊断。
func (e *Error) Error() string {
	return e.Message
}

// Unwrap 提供底层错误链供类别和取消判断使用。
// 返回值不适合作为用户输出，各入口仍应使用安全摘要。
func (e *Error) Unwrap() error {
	return e.Cause
}

// Wrap 构造带安全消息的应用错误。
// message 由调用方提供无密钥文本，cause 保留诊断但不序列化。
func Wrap(code Code, message string, cause error) *Error {
	return &Error{
		Code:    code,
		Message: message,
		Cause:   cause,
	}
}

// Failure 将任意错误转为可输出的应用错误。
// 已标记的写入结果未知优先于底层超时，裸取消转换为 CANCELLED。
func Failure(err error) *Error {
	if err == nil {
		return nil
	}
	var failure *Error
	if errors.As(err, &failure) {
		return failure
	}
	if errors.Is(err, context.Canceled) {
		return Wrap(Cancelled, "Operation cancelled.", err)
	}
	return Wrap(IOError, "Operation failed. Check the environment and try again.", err)
}

// ExitCode 按命令契约选择进程退出码。
// Git 透传可实现 ExitCode 方法保留原码，应用取消和未知分别为 130／4。
func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	if errors.Is(err, context.Canceled) {
		return 130
	}
	var failure *Error
	if errors.As(err, &failure) {
		switch failure.Code {
		case InvalidArgument, ConfigError, ConfigConflict, ContextRequired, ContextAmbiguous, ResourceTypeMismatch:
			return 2
		case Cancelled:
			return 130
		case PartialFailure:
			return 3
		case ResultUnknown:
			return 4
		default:
			return 1
		}
	}
	var process interface{ ExitCode() int }
	if errors.As(err, &process) {
		return process.ExitCode()
	}
	return 1
}
