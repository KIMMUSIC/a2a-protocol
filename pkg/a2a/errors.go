package a2a

const (
	ErrValidationFailed = "VALIDATION_FAILED"
	ErrTimeout          = "TIMEOUT"
	ErrUnauthorized     = "UNAUTHORIZED"
	ErrForbidden        = "FORBIDDEN"
	ErrNotFound         = "NOT_FOUND"
	ErrConflict         = "CONFLICT" // 멱등 충돌 등
	ErrInternal         = "INTERNAL"
)

type ErrorPayload struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Hint    string `json:"hint,omitempty"`
}

func NewError(code, msg string) *ErrorPayload {
	return &ErrorPayload{Code: code, Message: msg}
}
