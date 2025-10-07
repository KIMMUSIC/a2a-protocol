package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	a2a "a2a/contract"

	"github.com/go-chi/chi/v5"
	openai "github.com/sashabaranov/go-openai"
)

// ====== A2A task store ======
type store struct {
	mu sync.Mutex
	m  map[string]*a2a.Task
}

var st = store{m: map[string]*a2a.Task{}}

// ====== QUOTE.input 스키마 ======
type QuoteInput struct {
	From     map[string]any `json:"from"`
	To       map[string]any `json:"to"`
	Parcel   map[string]any `json:"parcel"`
	Options  map[string]any `json:"options,omitempty"`
	Currency string         `json:"currency,omitempty"`
	MaxWait  int            `json:"max_wait_ms,omitempty"`
}

// ====== LLM 클라이언트 ======
type LLMClient struct {
	c     *openai.Client
	model string
}

func newLLM() *LLMClient {
	cfg := openai.DefaultConfig(os.Getenv("OPENAI_API_KEY"))
	if base := os.Getenv("OPENAI_BASE_URL"); base != "" {
		cfg.BaseURL = base
	}
	return &LLMClient{
		c:     openai.NewClientWithConfig(cfg),
		model: getenv("OPENAI_MODEL", "gpt-4o-mini"),
	}
}

func main() {
	agentID := getenv("AGENT_ID", "agent.interpreter-go")

	r := chi.NewRouter()

	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) })

	// Discovery
	r.Get("/.well-known/agent.json", func(w http.ResponseWriter, _ *http.Request) {
		meta := a2a.AgentMeta{
			AgentID:     agentID,
			Name:        "Interpreter (Go LLM)",
			Version:     "0.2.0",
			ContractVer: a2a.ContractVersion,
			Capabilities: []a2a.AgentCapability{
				{TaskType: "INTERPRET", InputSchema: "Utterance", OutputSchema: "QuoteRequest"},
			},
			Auth: &a2a.AuthSpec{Required: false, Scheme: "HMAC"},
		}
		_ = json.NewEncoder(w).Encode(meta)
	})

	// CreateTask (INTERPRET)
	r.Post("/tasks", func(w http.ResponseWriter, r *http.Request) {
		var ct a2a.CreateTask
		if err := json.NewDecoder(r.Body).Decode(&ct); err != nil {
			w.WriteHeader(400)
			_ = json.NewEncoder(w).Encode(a2a.Task{Error: a2a.NewError(a2a.ErrValidationFailed, err.Error())})
			return
		}
		if ct.TaskType != "INTERPRET" {
			w.WriteHeader(400)
			_ = json.NewEncoder(w).Encode(a2a.Task{Error: a2a.NewError(a2a.ErrValidationFailed, "unsupported task_type")})
			return
		}

		var in struct {
			Utterance string `json:"utterance"`
		}
		if err := json.Unmarshal(ct.Input, &in); err != nil || strings.TrimSpace(in.Utterance) == "" {
			w.WriteHeader(400)
			_ = json.NewEncoder(w).Encode(a2a.Task{Error: a2a.NewError(a2a.ErrValidationFailed, "missing utterance")})
			return
		}

		// LLM 기반 해석 시도 → 실패 시 규칙기반 폴백
		var (
			out QuoteInput
			err error
		)
		if os.Getenv("OPENAI_API_KEY") != "" || os.Getenv("OPENAI_BASE_URL") != "" {
			out, err = interpretWithLLM(context.Background(), newLLM(), in.Utterance)
		} else {
			err = errors.New("no LLM configured")
		}
		if err != nil {
			log.Println("[Interpreter] LLM failed → fallback:", err)
			out = interpretFallback(in.Utterance)
		}

		// 기본값 보정
		if out.Currency == "" {
			out.Currency = "KRW"
		}
		if out.MaxWait == 0 {
			out.MaxWait = 1200
		}

		resultBytes, _ := json.Marshal(out)
		taskID := "t_interp_" + RandID()
		t := &a2a.Task{TaskID: taskID, Status: a2a.StatusSucceeded, Result: resultBytes}

		st.mu.Lock()
		st.m[taskID] = t
		st.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"task_id": taskID, "status": t.Status})
	})

	// GetTask
	r.Get("/tasks/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		st.mu.Lock()
		t, ok := st.m[id]
		st.mu.Unlock()
		if !ok {
			w.WriteHeader(404)
			_ = json.NewEncoder(w).Encode(a2a.NewError(a2a.ErrNotFound, "task not found"))
			return
		}
		_ = json.NewEncoder(w).Encode(t)
	})

	// 이벤트 수신(사용 안 함)
	r.Post("/tasks/{id}/events", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) })

	log.Println("Interpreter(LLM) listening :8083")
	_ = http.ListenAndServe(":8083", r)
}

// ====== LLM 해석 ======

func interpretWithLLM(ctx context.Context, llm *LLMClient, utterance string) (QuoteInput, error) {
	// JSON 전용 출력 요구 (OpenAI JSON 모드/함수호출 없이도 잘 동작)
	sys := `You are a shipping quote input parser.
Extract a strict JSON object matching this schema:
{
  "from": {"country": "ISO2", "postal": "string?"},
  "to":   {"country": "ISO2", "postal": "string?"},
  "parcel": {"weight_kg": number, "l_cm": number, "w_cm": number, "h_cm": number},
  "options": {"priority": boolean?},
  "currency": "KRW|USD|JPY|..." ,
  "max_wait_ms": number
}
Rules:
- Guess sensible defaults if unspecified (l=30,w=20,h=15, currency=KRW, max_wait_ms=1200).
- Countries: map '한국/대한민국/서울'→KR, '미국/샌프란시스코/USA'→US.
- If ambiguous, choose likely values; DO NOT add commentary; output JSON only.`

	usr := "Utterance: " + utterance

	req := openai.ChatCompletionRequest{
		Model: llm.model,
		Messages: []openai.ChatCompletionMessage{
			{Role: openai.ChatMessageRoleSystem, Content: sys},
			{Role: openai.ChatMessageRoleUser, Content: usr},
		},
		// JSON-mode: 최신 모델들은 ResponseFormat 설정 또는 시스템 지시로 JSON만 출력 유도
		ResponseFormat: &openai.ChatCompletionResponseFormat{
			Type: openai.ChatCompletionResponseFormatTypeJSONObject,
		},
		Temperature: 0.1,
	}

	// 간단 재시도
	var lastErr error
	for i := 0; i < 2; i++ {
		resp, err := llm.c.CreateChatCompletion(ctx, req)
		if err != nil {
			lastErr = err
			time.Sleep(300 * time.Millisecond)
			continue
		}
		if len(resp.Choices) == 0 {
			lastErr = errors.New("no choices")
			continue
		}
		txt := resp.Choices[0].Message.Content

		var out QuoteInput
		if err := json.Unmarshal([]byte(txt), &out); err != nil {
			lastErr = err
			continue
		}
		// 간단 검증
		if out.From == nil || out.To == nil || out.Parcel == nil {
			lastErr = errors.New("missing required fields")
			continue
		}
		return out, nil
	}
	return QuoteInput{}, lastErr
}

// ====== 규칙 기반 폴백 ======

func interpretFallback(u string) QuoteInput {
	s := strings.ToLower(u)
	weight := 1.0
	re := regexp.MustCompile(`(\d+(\.\d+)?)\s*kg`)
	if m := re.FindStringSubmatch(s); len(m) > 0 {
		var f float64
		_ = json.Unmarshal([]byte(m[1]), &f)
		weight = f
	}
	from := map[string]any{"country": "KR"}
	to := map[string]any{"country": "US"}
	if strings.Contains(s, "한국") || strings.Contains(s, "서울") {
		from["country"] = "KR"
	}
	if strings.Contains(s, "미국") || strings.Contains(s, "샌프란시스코") || strings.Contains(s, "usa") {
		to["country"] = "US"
	}
	priority := strings.Contains(s, "빠른") || strings.Contains(s, "급") || strings.Contains(s, "express")
	return QuoteInput{
		From:     from,
		To:       to,
		Parcel:   map[string]any{"weight_kg": weight, "l_cm": 30, "w_cm": 20, "h_cm": 15},
		Options:  map[string]any{"priority": priority},
		Currency: "KRW",
		MaxWait:  1200,
	}
}

// ====== 유틸 ======
func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
func RandID() string { return time.Now().Format("150405") } // 데모용
