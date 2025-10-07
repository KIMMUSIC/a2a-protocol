package a2a

const (
	HeaderAgentID     = "X-Agent-Id"           // 호출 주체 식별자
	HeaderSignature   = "X-Agent-Signature"    // hmac-sha256:<hex>
	HeaderTraceID     = "X-Agent-Trace-Id"     // 분산 추적
	HeaderRequestTime = "X-Agent-Request-Time" // RFC3339 or epoch-sec (옵션)
)
