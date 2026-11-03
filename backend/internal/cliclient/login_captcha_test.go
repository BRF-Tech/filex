package cliclient

import (
	"errors"
	"testing"
)

// A sign-in the bot check refused is told apart from a wrong password, so the
// CLI can say what to use instead (an API key).
func TestIsCaptchaRefusal(t *testing.T) {
	refused := &APIError{Status: 422, Code: "captcha_required", Body: []byte(`{"error":"captcha_required","captcha":"missing_token"}`)}
	if !IsCaptchaRefusal(refused) {
		t.Fatal("422 captcha not recognised")
	}
	fallback := &APIError{Status: 409, Code: "captcha_fallback", Body: []byte(`{"error":"captcha_fallback","captcha":"fallback","challenge":{}}`)}
	if !IsCaptchaRefusal(fallback) {
		t.Fatal("409 fallback not recognised")
	}
	if IsCaptchaRefusal(&APIError{Status: 401, Body: []byte(`{"error":"invalid credentials"}`)}) {
		t.Fatal("a wrong password is not the bot check")
	}
	if IsCaptchaRefusal(errors.New("network")) {
		t.Fatal("not an API error")
	}
}
