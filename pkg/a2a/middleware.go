package a2a

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"strconv"
	"time"
)

// SecretProvider: 호출 주체(AgentID)별 공유 비밀을 반환
type SecretProvider func(agentID string) (secret []byte, ok bool)

func HMACMiddleware(sp SecretProvider, clockSkew time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			agentID := r.Header.Get(HeaderAgentID)
			sig := r.Header.Get(HeaderSignature)
			ts := r.Header.Get(HeaderRequestTime)

			if agentID == "" || sig == "" {
				http.Error(w, "missing A2A headers", http.StatusUnauthorized)
				return
			}

			// 시계 오차 검사(선택)
			if ts != "" && clockSkew > 0 {
				t, ok := parseHeaderTime(ts)
				if ok {
					if d := time.Since(t); d > clockSkew || d < -clockSkew {
						http.Error(w, "request time skewed", http.StatusUnauthorized)
						return
					}
				}
			}

			secret, ok := sp(agentID)
			if !ok {
				http.Error(w, "unknown agent", http.StatusUnauthorized)
				return
			}

			// 바디 읽기 + 복원
			bodyBytes, err := io.ReadAll(r.Body)
			if err != nil {
				http.Error(w, "read body failed", http.StatusBadRequest)
				return
			}
			// 반드시 원상복구: 검증 후 다음 핸들러가 다시 읽을 수 있게
			defer func() { r.Body = io.NopCloser(bytes.NewReader(bodyBytes)) }()
			r.Body.Close()

			bodyHash := sha256.Sum256(bodyBytes)
			bodyHashHex := hex.EncodeToString(bodyHash[:])

			// canonical string: method + path + rawQuery + bodyHash + timestamp
			canon := r.Method + "\n" + r.URL.Path + "\n" + r.URL.RawQuery + "\n" + bodyHashHex + "\n" + ts

			if !VerifyHMACSHA256(secret, []byte(canon), stripAlgoPrefix(sig)) {
				http.Error(w, "invalid signature", http.StatusUnauthorized)
				return
			}

			// 핸들러에 복원된 바디 전달
			r.Body = io.NopCloser(bytes.NewReader(bodyBytes))
			next.ServeHTTP(w, r)
		})
	}
}

func stripAlgoPrefix(sig string) string {
	const p = "hmac-sha256:"
	if len(sig) > len(p) && sig[:len(p)] == p {
		return sig[len(p):]
	}
	return sig
}

func parseHeaderTime(v string) (time.Time, bool) {
	// 1) RFC3339 시도
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return t, true
	}
	// 2) unix seconds 시도
	if sec, err := strconv.ParseInt(v, 10, 64); err == nil {
		return time.Unix(sec, 0).UTC(), true
	}
	return time.Time{}, false
}
