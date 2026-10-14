package middleware_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Trisentosa/payment-module/internal/adapter/inbound/http/middleware"
	"github.com/Trisentosa/payment-module/internal/pkg/apikey"
)

const orderKey = "pgk_order-service-test-key"

// callerAuthed runs a request through CallerAuth and reports the caller the handler saw.
func callerAuthed(t *testing.T, header map[string]string) (*httptest.ResponseRecorder, string) {
	t.Helper()
	seen := ""
	h := middleware.CallerAuth(map[string]string{apikey.Hash(orderKey): "order-service"})(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			seen = middleware.CallerService(r.Context())
			w.WriteHeader(http.StatusNoContent)
		}))
	req := httptest.NewRequest(http.MethodGet, "/payments", nil)
	for k, v := range header {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec, seen
}

func TestCallerAuth(t *testing.T) {
	cases := []struct {
		name       string
		header     map[string]string
		wantStatus int
		wantCaller string
	}{
		{"valid key", map[string]string{"Authorization": "Bearer " + orderKey}, http.StatusNoContent, "order-service"},
		{"scheme is case-insensitive", map[string]string{"Authorization": "bearer " + orderKey}, http.StatusNoContent, "order-service"},
		{"matching X-Service-Name", map[string]string{"Authorization": "Bearer " + orderKey, "X-Service-Name": "order-service"}, http.StatusNoContent, "order-service"},
		{"no credentials", nil, http.StatusUnauthorized, ""},
		{"header alone proves nothing", map[string]string{"X-Service-Name": "order-service"}, http.StatusUnauthorized, ""},
		{"unknown key", map[string]string{"Authorization": "Bearer pgk_guess"}, http.StatusUnauthorized, ""},
		{"wrong scheme", map[string]string{"Authorization": "Basic " + orderKey}, http.StatusUnauthorized, ""},
		{"key for another identity", map[string]string{"Authorization": "Bearer " + orderKey, "X-Service-Name": "billing-service"}, http.StatusForbidden, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec, caller := callerAuthed(t, tc.header)
			if rec.Code != tc.wantStatus || caller != tc.wantCaller {
				t.Errorf("status=%d caller=%q body=%s; want %d %q", rec.Code, caller, rec.Body, tc.wantStatus, tc.wantCaller)
			}
		})
	}
}

func TestAdminAuth(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	cases := []struct {
		name, configured, sent string
		want                   int
	}{
		{"right key", "admin-key-0123456789-0123456789-ab", "admin-key-0123456789-0123456789-ab", http.StatusNoContent},
		{"wrong key", "admin-key-0123456789-0123456789-ab", "admin-key", http.StatusUnauthorized},
		{"missing header", "admin-key-0123456789-0123456789-ab", "", http.StatusUnauthorized},
		// Regression: an unset key used to match a request with no header at all.
		{"unset key rejects everyone", "", "", http.StatusUnauthorized},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/payments", nil)
			if tc.sent != "" {
				req.Header.Set("X-Admin-Key", tc.sent)
			}
			rec := httptest.NewRecorder()
			middleware.AdminAuth(tc.configured)(ok).ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Errorf("status %d, want %d", rec.Code, tc.want)
			}
		})
	}
}

func TestRequestLogger_LogsAuthenticatedCaller(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, nil))
	inner := middleware.CallerAuth(map[string]string{apikey.Hash(orderKey): "order-service"})(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))

	req := httptest.NewRequest(http.MethodGet, "/payments", nil)
	req.Header.Set("Authorization", "Bearer "+orderKey)
	middleware.RequestLogger(log)(inner).ServeHTTP(httptest.NewRecorder(), req)

	var line map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &line); err != nil {
		t.Fatalf("decode log line %q: %v", buf.String(), err)
	}
	if line["msg"] != "request completed" || line["caller_service"] != "order-service" {
		t.Errorf("log line: %v", line)
	}
}
