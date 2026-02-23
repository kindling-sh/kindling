package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// ════════════════════════════════════════════════════════════════════
// jsonResponse
// ════════════════════════════════════════════════════════════════════

func TestJsonResponse(t *testing.T) {
	t.Run("sets_content_type", func(t *testing.T) {
		w := httptest.NewRecorder()
		jsonResponse(w, map[string]string{"status": "ok"})

		ct := w.Header().Get("Content-Type")
		if ct != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", ct)
		}
	})

	t.Run("encodes_map", func(t *testing.T) {
		w := httptest.NewRecorder()
		jsonResponse(w, map[string]string{"key": "value"})

		var result map[string]string
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatalf("invalid JSON: %v", err)
		}
		if result["key"] != "value" {
			t.Errorf("got key=%q, want %q", result["key"], "value")
		}
	})

	t.Run("encodes_struct", func(t *testing.T) {
		w := httptest.NewRecorder()
		data := struct {
			Name  string `json:"name"`
			Count int    `json:"count"`
		}{"test", 42}
		jsonResponse(w, data)

		var result map[string]interface{}
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatalf("invalid JSON: %v", err)
		}
		if result["name"] != "test" {
			t.Errorf("got name=%v, want %q", result["name"], "test")
		}
		if result["count"] != float64(42) {
			t.Errorf("got count=%v, want 42", result["count"])
		}
	})

	t.Run("default_status_200", func(t *testing.T) {
		w := httptest.NewRecorder()
		jsonResponse(w, "ok")

		if w.Code != http.StatusOK {
			t.Errorf("status = %d, want 200", w.Code)
		}
	})
}

// ════════════════════════════════════════════════════════════════════
// jsonError
// ════════════════════════════════════════════════════════════════════

func TestJsonError(t *testing.T) {
	t.Run("sets_content_type", func(t *testing.T) {
		w := httptest.NewRecorder()
		jsonError(w, "something went wrong", 500)

		ct := w.Header().Get("Content-Type")
		if ct != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", ct)
		}
	})

	t.Run("sets_status_code", func(t *testing.T) {
		w := httptest.NewRecorder()
		jsonError(w, "not found", 404)

		if w.Code != 404 {
			t.Errorf("status = %d, want 404", w.Code)
		}
	})

	t.Run("encodes_error_message", func(t *testing.T) {
		w := httptest.NewRecorder()
		jsonError(w, "bad request", 400)

		var result map[string]string
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatalf("invalid JSON: %v", err)
		}
		if result["error"] != "bad request" {
			t.Errorf("got error=%q, want %q", result["error"], "bad request")
		}
	})

	t.Run("various_status_codes", func(t *testing.T) {
		codes := []int{400, 401, 403, 404, 405, 500, 502, 503}
		for _, code := range codes {
			w := httptest.NewRecorder()
			jsonError(w, "err", code)
			if w.Code != code {
				t.Errorf("jsonError(_, _, %d) set status %d", code, w.Code)
			}
		}
	})
}

// ════════════════════════════════════════════════════════════════════
// actionOK
// ════════════════════════════════════════════════════════════════════

func TestActionOK(t *testing.T) {
	t.Run("returns_ok_true", func(t *testing.T) {
		w := httptest.NewRecorder()
		actionOK(w, "deployment created")

		var result actionResult
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatalf("invalid JSON: %v", err)
		}
		if !result.OK {
			t.Error("actionOK should set OK=true")
		}
		if result.Output != "deployment created" {
			t.Errorf("Output = %q, want %q", result.Output, "deployment created")
		}
		if result.Error != "" {
			t.Errorf("Error should be empty, got %q", result.Error)
		}
	})

	t.Run("status_200", func(t *testing.T) {
		w := httptest.NewRecorder()
		actionOK(w, "ok")
		if w.Code != http.StatusOK {
			t.Errorf("status = %d, want 200", w.Code)
		}
	})

	t.Run("empty_output", func(t *testing.T) {
		w := httptest.NewRecorder()
		actionOK(w, "")

		var result actionResult
		json.Unmarshal(w.Body.Bytes(), &result)
		if !result.OK {
			t.Error("actionOK with empty output should still be OK")
		}
	})
}

// ════════════════════════════════════════════════════════════════════
// actionErr
// ════════════════════════════════════════════════════════════════════

func TestActionErr(t *testing.T) {
	t.Run("returns_ok_false", func(t *testing.T) {
		w := httptest.NewRecorder()
		actionErr(w, "failed to deploy", 500)

		var result actionResult
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatalf("invalid JSON: %v", err)
		}
		if result.OK {
			t.Error("actionErr should set OK=false")
		}
		if result.Error != "failed to deploy" {
			t.Errorf("Error = %q, want %q", result.Error, "failed to deploy")
		}
		if result.Output != "" {
			t.Errorf("Output should be empty, got %q", result.Output)
		}
	})

	t.Run("sets_status_code", func(t *testing.T) {
		w := httptest.NewRecorder()
		actionErr(w, "bad", 422)
		if w.Code != 422 {
			t.Errorf("status = %d, want 422", w.Code)
		}
	})

	t.Run("content_type_json", func(t *testing.T) {
		w := httptest.NewRecorder()
		actionErr(w, "err", 400)
		ct := w.Header().Get("Content-Type")
		if ct != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", ct)
		}
	})
}

// ════════════════════════════════════════════════════════════════════
// requireMethod
// ════════════════════════════════════════════════════════════════════

func TestRequireMethod(t *testing.T) {
	t.Run("matching_method", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/api/deploy", nil)

		ok := requireMethod(w, r, http.MethodPost)
		if !ok {
			t.Error("requireMethod should return true for matching method")
		}
		// Should NOT set an error status
		if w.Code != http.StatusOK {
			t.Errorf("status = %d, want 200 (no error written)", w.Code)
		}
	})

	t.Run("mismatched_method", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/api/deploy", nil)

		ok := requireMethod(w, r, http.MethodPost)
		if ok {
			t.Error("requireMethod should return false for mismatched method")
		}
		if w.Code != http.StatusMethodNotAllowed {
			t.Errorf("status = %d, want 405", w.Code)
		}

		// Should write error JSON
		var result actionResult
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatalf("invalid JSON: %v", err)
		}
		if result.OK {
			t.Error("actionResult.OK should be false")
		}
		if result.Error != "method not allowed" {
			t.Errorf("Error = %q, want %q", result.Error, "method not allowed")
		}
	})

	t.Run("get_for_get", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/api/status", nil)
		ok := requireMethod(w, r, http.MethodGet)
		if !ok {
			t.Error("GET == GET should return true")
		}
	})

	t.Run("delete_for_post", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodDelete, "/api/delete", nil)
		ok := requireMethod(w, r, http.MethodPost)
		if ok {
			t.Error("DELETE != POST should return false")
		}
		if w.Code != http.StatusMethodNotAllowed {
			t.Errorf("status = %d, want 405", w.Code)
		}
	})
}

// ════════════════════════════════════════════════════════════════════
// actionResult struct JSON encoding
// ════════════════════════════════════════════════════════════════════

func TestActionResult_JSONOmitEmpty(t *testing.T) {
	t.Run("ok_with_output", func(t *testing.T) {
		r := actionResult{OK: true, Output: "done"}
		data, _ := json.Marshal(r)
		var m map[string]interface{}
		json.Unmarshal(data, &m)

		if _, hasError := m["error"]; hasError {
			t.Error("error field should be omitted when empty")
		}
	})

	t.Run("error_without_output", func(t *testing.T) {
		r := actionResult{OK: false, Error: "fail"}
		data, _ := json.Marshal(r)
		var m map[string]interface{}
		json.Unmarshal(data, &m)

		if _, hasOutput := m["output"]; hasOutput {
			t.Error("output field should be omitted when empty")
		}
	})
}
