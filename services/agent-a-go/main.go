package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"sync"

	a2a "a2a/contract"

	"github.com/go-chi/chi/v5"
)

type store struct {
	mu sync.Mutex
	m  map[string]*a2a.Task
}

var st = store{m: map[string]*a2a.Task{}}

func main() {
	agentID := env("AGENT_ID", "carrier.agent-a")
	//secret := os.Getenv("A2A_SECRET")

	r := chi.NewRouter()
	// HMAC 미들웨어(수신 검증) — 데모 단계에서는 일단 꺼두고 시작해도 됨
	// r.Use(a2a.HMACMiddleware(func(id string) ([]byte, bool) { return []byte(secret), true }, 2*time.Minute))

	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) })

	r.Get("/.well-known/agent.json", func(w http.ResponseWriter, _ *http.Request) {
		meta := a2a.AgentMeta{
			AgentID: agentID, Name: "Agent-A (Go)", Version: "0.1.0",
			ContractVer: a2a.ContractVersion,
			Capabilities: []a2a.AgentCapability{
				{TaskType: "QUOTE", InputSchema: "QuoteRequest", OutputSchema: "QuoteResult"},
				{TaskType: "SHIP", InputSchema: "ShipRequest", OutputSchema: "ShipResult"},
			},
			Auth: &a2a.AuthSpec{Required: false, Scheme: "HMAC"},
		}
		json.NewEncoder(w).Encode(meta)
	})

	r.Post("/tasks", func(w http.ResponseWriter, r *http.Request) {
		var ct a2a.CreateTask
		if err := json.NewDecoder(r.Body).Decode(&ct); err != nil {
			w.WriteHeader(400)
			json.NewEncoder(w).Encode(a2a.Task{Error: a2a.NewError(a2a.ErrValidationFailed, err.Error())})
			return
		}
		if err := a2a.ValidateCreateTask(&ct); err != nil {
			w.WriteHeader(400)
			json.NewEncoder(w).Encode(a2a.Task{Error: a2a.NewError(a2a.ErrValidationFailed, err.Error())})
			return
		}
		taskID := "t_" + RandID()
		t := &a2a.Task{TaskID: taskID, Status: a2a.StatusSucceeded} // QUOTE/SHIP 동기 처리(Agent-A는 동기)
		switch ct.TaskType {
		case "QUOTE":
			result := map[string]any{"carrier": "AgentA", "service": "EXPRESS", "price": 7000 + 1500*2, "eta_days": 2}
			b, _ := json.Marshal(result)
			t.Result = b
		case "SHIP":
			result := map[string]any{"status": "READY", "tracking_id": "A-" + RandID(), "label_url": "https://cdn.local/A.png"}
			b, _ := json.Marshal(result)
			t.Result = b
		default:
			t.Status = a2a.StatusFailed
			t.Error = a2a.NewError(a2a.ErrValidationFailed, "unsupported task_type")
		}
		st.mu.Lock()
		st.m[taskID] = t
		st.mu.Unlock()
		json.NewEncoder(w).Encode(map[string]any{"task_id": taskID, "status": t.Status})
	})

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

	// 비동기 이벤트 수신 엔드포인트(에이전트 입장에선 보통 사용 X — 형태만 제공)
	r.Post("/tasks/{id}/events", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) })

	log.Println("Agent-A listening :8081")
	http.ListenAndServe(":8081", r)
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
func RandID() string { return "123456" } // TODO: uuid로 교체
