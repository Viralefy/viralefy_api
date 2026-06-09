package senderclient

// Stubs de teste — suite real (httptest.Server + assertion de
// X-Internal-Token + payload shape) será adicionada na Wave 3 quando o
// microservice viralefy_sender expuser o handler /internal/v1/send.

import "testing"

func TestNewReturnsClient(t *testing.T) {
	c := New("http://127.0.0.1:8082/", "tok")
	if c == nil {
		t.Fatal("New returned nil")
	}
	if c.baseURL != "http://127.0.0.1:8082" {
		t.Fatalf("trailing slash not trimmed: %q", c.baseURL)
	}
}
