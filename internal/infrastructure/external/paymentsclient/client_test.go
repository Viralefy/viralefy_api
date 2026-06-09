package paymentsclient

// Stubs de teste — a suite real (httptest.Server + assertion de
// X-Internal-Token + payload shape) será adicionada na Wave 3 quando o
// microservice viralefy_payments expuser os handlers /internal/v1/charge
// e /internal/v1/methods. Por ora só garantimos que o pacote compila.

import "testing"

func TestNewReturnsClient(t *testing.T) {
	c := New("http://127.0.0.1:8081/", "tok")
	if c == nil {
		t.Fatal("New returned nil")
	}
	if c.baseURL != "http://127.0.0.1:8081" {
		t.Fatalf("trailing slash not trimmed: %q", c.baseURL)
	}
	if c.Provider() != "remote" {
		t.Fatalf("Provider() = %q, want %q", c.Provider(), "remote")
	}
}
