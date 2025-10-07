package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sync"
	"time"

	a2a "a2a/contract"

	"github.com/go-chi/chi/v5"
)

type store struct {
	mu sync.Mutex
	m  map[string]*a2a.Task
}

var st = store{m: map[string]*a2a.Task{}}

var agentA = env("AGENT_A_URL", "http://localhost:8081")
var agentB = env("AGENT_B_URL", "http://localhost:8082")
var interpreter = env("INTERPRETER_URL", "http://localhost:8083")

func main() {
	r := chi.NewRouter()
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) })

	// Discovery(부팅 로그용 — 실패해도 동작엔 영향 없음)
	go discover(agentA)
	go discover(agentB)

	// CreateTask(QUOTE/SHIP)
	r.Post("/tasks", func(w http.ResponseWriter, r *http.Request) {
		var ct a2a.CreateTask
		if err := json.NewDecoder(r.Body).Decode(&ct); err != nil {
			w.WriteHeader(400)
			json.NewEncoder(w).Encode(a2a.NewError(a2a.ErrValidationFailed, err.Error()))
			return
		}
		taskID := "t_" + RandID()

		switch ct.TaskType {
		case "QUOTE":
			// 1) 입력 검사
			var raw map[string]any
			_ = json.Unmarshal(ct.Input, &raw)
			needsInterpret := false
			if _, ok := raw["utterance"]; ok {
				needsInterpret = true
			} else {
				// 필수 필드가 없으면 해석 필요
				if _, ok := raw["from"]; !ok {
					needsInterpret = true
				}
				if _, ok := raw["to"]; !ok {
					needsInterpret = true
				}
				if _, ok := raw["parcel"]; !ok {
					needsInterpret = true
				}
			}

			// 2) 해석 단계
			quoteInput := ct.Input
			if needsInterpret {
				interpOut, err := postInterpret(interpreter, raw)
				if err != nil {
					w.WriteHeader(400)
					json.NewEncoder(w).Encode(a2a.NewError(a2a.ErrValidationFailed, "interpret failed: "+err.Error()))
					return
				}
				quoteInput = interpOut // 구조화된 QUOTE.input JSON
			}
			// fan-out to Agent-A/B
			ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
			defer cancel()
			type qres struct {
				ok   bool
				data map[string]any
				err  error
			}
			ch := make(chan qres, 2)
			go func() { q, err := postTask(agentA, "QUOTE", quoteInput); ch <- qres{ok: err == nil, data: q, err: err} }()
			go func() { q, err := postTask(agentB, "QUOTE", quoteInput); ch <- qres{ok: err == nil, data: q, err: err} }()

			var quotes []map[string]any
			timeout := time.After(1800 * time.Millisecond)
		loop:
			for i := 0; i < 2; i++ {
				select {
				case r := <-ch:
					if r.ok {
						quotes = append(quotes, r.data)
					}
				case <-timeout:
					break loop
				case <-ctx.Done():
					break loop
				}
			}
			// 결과 저장
			res := map[string]any{"quotes": quotes, "partial_failures": []any{}}
			b, _ := json.Marshal(res)
			t := &a2a.Task{TaskID: taskID, Status: a2a.StatusSucceeded, Result: b}
			st.mu.Lock()
			st.m[taskID] = t
			st.mu.Unlock()
			json.NewEncoder(w).Encode(map[string]any{"task_id": taskID, "status": t.Status})

		case "SHIP":
			// 단순 위임(여기서는 Agent-A로 위임 예)
			q, err := postTask(agentA, "SHIP", ct.Input)
			status := a2a.StatusSucceeded
			var result any = q
			if err != nil {
				status = a2a.StatusFailed
				result = a2a.NewError(a2a.ErrInternal, err.Error())
			}
			b, _ := json.Marshal(result)
			t := &a2a.Task{TaskID: taskID, Status: status, Result: b}
			st.mu.Lock()
			st.m[taskID] = t
			st.mu.Unlock()
			json.NewEncoder(w).Encode(map[string]any{"task_id": taskID, "status": status})
		default:
			w.WriteHeader(400)
			json.NewEncoder(w).Encode(a2a.NewError(a2a.ErrValidationFailed, "unsupported task_type"))
		}
	})

	// GetTask
	r.Get("/tasks/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		st.mu.Lock()
		t, ok := st.m[id]
		st.mu.Unlock()
		if !ok {
			w.WriteHeader(404)
			json.NewEncoder(w).Encode(a2a.NewError(a2a.ErrNotFound, "task not found"))
			return
		}
		json.NewEncoder(w).Encode(t)
	})

	// Event 수신(비동기 완료시)
	r.Post("/tasks/{id}/events", func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		body, _ := io.ReadAll(r.Body)
		var ev a2a.Event
		if err := json.Unmarshal(body, &ev); err == nil && ev.Event == "TASK_COMPLETED" {
			// 완료 반영
			st.mu.Lock()
			if t, ok := st.m[id]; ok {
				t.Status = a2a.StatusSucceeded
				t.Result = ev.Payload
			}
			st.mu.Unlock()
		}
		w.WriteHeader(204)
	})

	log.Println("Concierge listening :8080")
	http.ListenAndServe(":8080", r)
}

func postTask(baseURL, taskType string, input json.RawMessage) (map[string]any, error) {
	req := a2a.CreateTask{TaskType: taskType, Input: input}
	b, _ := json.Marshal(req)
	resp, err := http.Post(baseURL+"/tasks", "application/json", bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	// 단순화를 위해: 에이전트가 동기 결과를 곧장 반환한다고 가정하고 /tasks/{id}를 생략
	// (실제에선 id 받아서 /tasks/{id} 조회 or 콜백을 기다림)
	var rmap map[string]any
	json.NewDecoder(resp.Body).Decode(&rmap)
	// Agent가 즉시 SUCCEEDED로 저장했다면 /tasks/{id}에서 결과 가져와도 됨
	// 여기선 간단 케이스로 agent의 동기 계산 결과를 한 번 더 GET 하지 않고 사용
	switch taskType {
	case "QUOTE":
		// 데모용: Agent도 동기 작성이라면 /tasks/{id} 없이 바로 QuoteResult를 반환하도록 만들어도 됨
		return rmap, nil
	default:
		return rmap, nil
	}
}

func discover(base string) {
	resp, err := http.Get(base + "/.well-known/agent.json")
	if err != nil {
		log.Println("discovery error:", base, err)
		return
	}
	defer resp.Body.Close()
	var meta a2a.AgentMeta
	json.NewDecoder(resp.Body).Decode(&meta)
	log.Printf("discovered: %s capabilities=%v\n", meta.AgentID, meta.Capabilities)
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
func RandID() string { return "654321" } // TODO: uuid 교체

func postInterpret(baseURL string, userInput map[string]any) (json.RawMessage, error) {
	// userInput에 utterance가 없다면, 간단히 하나 만들어 LLM/규칙 파서로 넘겨도 됨
	if _, ok := userInput["utterance"]; !ok {
		// 문자열 합치기 (데모용)
		b, _ := json.Marshal(userInput)
		userInput = map[string]any{"utterance": string(b)}
	}
	// INTERPRET 태스크 전송
	body := a2a.CreateTask{TaskType: "INTERPRET"}
	bIn, _ := json.Marshal(map[string]any{"utterance": userInput["utterance"]})
	body.Input = bIn
	b, _ := json.Marshal(body)

	resp, err := http.Post(baseURL+"/tasks", "application/json", bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var ack struct {
		TaskID string `json:"task_id"`
		Status string `json:"status"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&ack); err != nil {
		return nil, err
	}

	// 곧바로 결과 조회
	gr, err := http.Get(baseURL + "/tasks/" + ack.TaskID)
	if err != nil {
		return nil, err
	}
	defer gr.Body.Close()
	var t a2a.Task
	if err := json.NewDecoder(gr.Body).Decode(&t); err != nil {
		return nil, err
	}
	if t.Status != a2a.StatusSucceeded {
		return nil, fmt.Errorf("interpreter status=%s", t.Status)
	}
	return t.Result, nil
}
