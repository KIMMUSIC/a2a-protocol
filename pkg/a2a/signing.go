package a2a

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
)

// Canonical string을 HMAC-SHA256으로 서명/검증
func MakeHMACSHA256(secret []byte, payload []byte) string {
	m := hmac.New(sha256.New, secret)
	m.Write(payload)
	return hex.EncodeToString(m.Sum(nil))
}

func VerifyHMACSHA256(secret, payload []byte, hexSig string) bool {
	expect := MakeHMACSHA256(secret, payload)
	// 고정 시간 비교
	if len(expect) != len(hexSig) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(expect), []byte(hexSig)) == 1
}

// A2A 권장 canonical string 예시:
// method + "\n" + path + "\n" + rawQuery + "\n" + bodySha256Hex + "\n" + timestamp
//
// 이 함수는 bodySha256Hex까지 계산해 canonical 문자열을 만들어줍니다.
func CanonicalString(method, path, rawQuery, body string, timestamp string) string {
	// body 해시를 포함시키면 중간자 공격 및 본문 변조 방지에 유리
	h := sha256.Sum256([]byte(body))
	return method + "\n" + path + "\n" + rawQuery + "\n" + hex.EncodeToString(h[:]) + "\n" + timestamp
}
