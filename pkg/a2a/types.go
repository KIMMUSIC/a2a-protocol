package a2a

import "encoding/json"

const ContractVersion = "1.0"

// ---- Task lifecycle ---------------------------------------------------------

type TaskStatus string

const (
	StatusPending   TaskStatus = "PENDING"
	StatusRunning   TaskStatus = "RUNNING"
	StatusSucceeded TaskStatus = "SUCCEEDED"
	StatusFailed    TaskStatus = "FAILED"
)

// CreateTask: 다른 에이전트에게 작업을 위임할 때 사용하는 표준 입력
type CreateTask struct {
	TaskType       string          `json:"task_type"`                 // e.g. QUOTE | SHIP | RANK | INTERPRET
	Input          json.RawMessage `json:"input"`                     // 도메인별 입력(JSON blob)
	ReplyURL       string          `json:"reply_url,omitempty"`       // 콜백 받을 URL(옵션)
	IdempotencyKey string          `json:"idempotency_key,omitempty"` // 멱등 처리용
	Meta           map[string]any  `json:"meta,omitempty"`            // 추가 컨텍스트(옵션)
}

// Task: 작업의 현재 상태/결과/오류를 나타내는 표준 출력
type Task struct {
	TaskID string          `json:"task_id"`
	Status TaskStatus      `json:"status"`
	Result json.RawMessage `json:"result,omitempty"` // 도메인별 결과(JSON blob)
	Error  *ErrorPayload   `json:"error,omitempty"`
}

// ---- Agent discovery ---------------------------------------------------------

type AgentMeta struct {
	AgentID      string            `json:"agent_id"`
	Name         string            `json:"name"`
	Version      string            `json:"version"`          // 구현체 버전
	ContractVer  string            `json:"contract_version"` // A2A 계약 버전 (1.0)
	Capabilities []AgentCapability `json:"capabilities"`
	Auth         *AuthSpec         `json:"auth,omitempty"`
}

type AgentCapability struct {
	TaskType     string `json:"task_type"`     // e.g. QUOTE
	InputSchema  string `json:"input_schema"`  // 문서/스키마 식별자(설명 또는 URL 가능)
	OutputSchema string `json:"output_schema"` // 문서/스키마 식별자
}

type AuthSpec struct {
	Required bool   `json:"required"`
	Scheme   string `json:"scheme"` // "HMAC" | "mTLS" | "None"
}

// ---- Task events (async callbacks) ------------------------------------------

type Event struct {
	Event   string          `json:"event"` // e.g. TASK_COMPLETED | TASK_FAILED | TASK_PROGRESS
	TaskID  string          `json:"task_id"`
	Payload json.RawMessage `json:"payload,omitempty"` // 결과/중간상태
}
