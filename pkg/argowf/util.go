package argowf

import (
	"encoding/json"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

func mustJSON(v any) []byte {
	raw, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return raw
}

func isNotFound(err error) bool {
	return apierrors.IsNotFound(err)
}

func isAlreadyExists(err error) bool {
	return apierrors.IsAlreadyExists(err)
}

func safeName(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	lastDash := false
	for _, r := range s {
		ok := r >= 'a' && r <= 'z' || r >= '0' && r <= '9'
		if ok {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "unknown"
	}
	if len(out) > 45 {
		return strings.Trim(out[:45], "-")
	}
	return out
}
