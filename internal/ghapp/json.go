package ghapp

import "encoding/json"

// apiMessage returns the message in a GitHub error response. It returns an empty string if the body is not JSON or has no message.
func apiMessage(body []byte) string {
	var e struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &e); err != nil {
		return ""
	}
	return e.Message
}
