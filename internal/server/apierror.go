package server

import (
	"errors"
	"net/http"
	"time"
)

// 接口错误的统一表示与错误码目录（设计 19.0.2、43.4）。
//
// 业务代码只返回具名错误（*APIError），HTTP 状态码与响应格式在 writeError 中统一映射，
// 业务代码不直接写状态码（设计 43.3.1）。

// Code 是稳定的英文错误码，客户端据此分支处理；一经发布不再改名（设计 43.4）。
type Code string

// 错误码目录与设计 43.4 的表格一一对应。message 为中文默认提示，可直接展示给用户（设计 41.4.2）。
const (
	CodeValidationFailed  Code = "validation_failed"
	CodeBadRequest        Code = "bad_request"
	CodeUnauthorized      Code = "unauthorized"
	CodeTokenRevoked      Code = "token_revoked"
	CodeForbidden         Code = "forbidden"
	CodeNotFound          Code = "not_found"
	CodeConflict          Code = "conflict"
	CodePayloadTooLarge   Code = "payload_too_large"
	CodeEnrollCodeInvalid Code = "enroll_code_invalid"
	CodeRateLimited       Code = "rate_limited"
	CodeQuotaExceeded     Code = "quota_exceeded"
	CodeUnavailable       Code = "unavailable"
	CodeInternal          Code = "internal"
	// 以下两个在设计 43.4 的表格之外补充（修订记录第 23 条）
	CodePasswordChangeRequired Code = "password_change_required"
	CodeReauthRequired         Code = "reauth_required"
	CodeCaptchaFailed          Code = "captcha_failed" // 修订记录第 24 条
	CodeUnsupportedEncoding    Code = "unsupported_encoding"
)

// codeInfo 是每个错误码对应的 HTTP 状态与默认中文提示。
var codeInfo = map[Code]struct {
	status  int
	message string
}{
	CodeValidationFailed:       {http.StatusUnprocessableEntity, "请检查填写的内容"},
	CodeBadRequest:             {http.StatusBadRequest, "请求格式不正确"},
	CodeUnauthorized:           {http.StatusUnauthorized, "登录已失效，请重新登录"},
	CodeTokenRevoked:           {http.StatusUnauthorized, "凭证已被吊销"},
	CodeForbidden:              {http.StatusForbidden, "没有权限执行此操作"},
	CodeNotFound:               {http.StatusNotFound, "请求的资源不存在"},
	CodeConflict:               {http.StatusConflict, "数据已被修改或名称已被使用，请刷新后重试"},
	CodePayloadTooLarge:        {http.StatusRequestEntityTooLarge, "数据过大"},
	CodeEnrollCodeInvalid:      {http.StatusBadRequest, "注册码无效或已过期"},
	CodeRateLimited:            {http.StatusTooManyRequests, "操作过于频繁，请稍后再试"},
	CodeQuotaExceeded:          {http.StatusConflict, "本周期流量已接近上限，操作已被禁止"},
	CodeUnavailable:            {http.StatusServiceUnavailable, "服务暂时不可用，请稍后重试"},
	CodeInternal:               {http.StatusInternalServerError, "服务器内部错误"},
	CodePasswordChangeRequired: {http.StatusForbidden, "请先修改初始密码"},
	CodeReauthRequired:         {http.StatusForbidden, "请重新输入密码以确认此操作"},
	CodeCaptchaFailed:          {http.StatusBadRequest, "滑块验证未通过，请重试"},
	CodeUnsupportedEncoding:    {http.StatusUnsupportedMediaType, "不支持的内容编码"},
}

// FieldError 是表单字段级错误，放在响应的 details 中，Web 在对应字段下显示（设计 43.6）。
type FieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// APIError 是返回给客户端的具名错误。
//
// 【安全】Message 与 Details 会原样返回客户端，不得包含 SQL、文件路径、堆栈、内部地址（设计 43.3.1）；
// 排障信息放进 Cause，只写入服务端日志。
type APIError struct {
	Code       Code
	Message    string        // 为空时使用错误码的默认提示
	Details    []FieldError  // 仅 validation_failed 使用
	Cause      error         // 内部原因，只记录日志，不返回客户端
	RetryAfter time.Duration // 仅 rate_limited 使用，写入 Retry-After 响应头（设计 43.2）
}

func (e *APIError) Error() string {
	if e.Cause != nil {
		return string(e.Code) + ": " + e.Cause.Error()
	}
	return string(e.Code)
}

func (e *APIError) Unwrap() error { return e.Cause }

// Status 返回该错误对应的 HTTP 状态码。
func (e *APIError) Status() int {
	if info, ok := codeInfo[e.Code]; ok {
		return info.status
	}
	return http.StatusInternalServerError
}

// errorf 创建一个具名错误；message 为空时使用默认提示。
func errorf(code Code, message string) *APIError {
	return &APIError{Code: code, Message: message}
}

// internalError 把未预期的错误包装为 internal，原因只进日志。
func internalError(cause error) *APIError {
	return &APIError{Code: CodeInternal, Cause: cause}
}

// errorBody 是错误响应体：{"error": {"code", "message", "request_id", "details"}}（设计 43.4）。
type errorBody struct {
	Error errorPayload `json:"error"`
}

type errorPayload struct {
	Code      Code         `json:"code"`
	Message   string       `json:"message"`
	RequestID string       `json:"request_id"`
	Details   []FieldError `json:"details,omitempty"`
}

// asAPIError 把任意错误归一为 *APIError：具名错误原样返回，
// 请求体超过 MaxBytesReader 上限映射为 413，其余一律视为内部错误。
func asAPIError(err error) *APIError {
	var ae *APIError
	if errors.As(err, &ae) {
		return ae
	}
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		return &APIError{Code: CodePayloadTooLarge, Cause: err}
	}
	return internalError(err)
}
