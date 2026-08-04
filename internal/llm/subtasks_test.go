package llm

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestChatClientGenerateSubTasks(t *testing.T) {
	t.Helper()

	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
		}
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"choices":[{"message":{"content":"{\"subtasks\":[{\"label\":\"Write tests\",\"description\":\"Cover the happy path\"},{\"label\":\"Ship it\"}]}"}}]}`)
	}))
	defer srv.Close()

	client := &ChatClient{BaseURL: srv.URL, Model: "test", http: srv.Client()}

	out, err := client.GenerateSubTasks(SubTaskGenerationInput{
		Title:            "Ship feature",
		Description:      "Make it work",
		Body:             "body context",
		ExistingSubtasks: []string{"old"},
		MaxSubtasks:      5,
	})
	if err != nil {
		t.Fatalf("GenerateSubTasks: %v", err)
	}

	if len(out.Subtasks) != 2 {
		t.Fatalf("expected 2 subtasks, got %d", len(out.Subtasks))
	}
	if out.Subtasks[0].Label != "Write tests" {
		t.Errorf("expected label %q, got %q", "Write tests", out.Subtasks[0].Label)
	}
	if out.Subtasks[0].Description != "Cover the happy path" {
		t.Errorf("expected description %q, got %q", "Cover the happy path", out.Subtasks[0].Description)
	}
	if out.Subtasks[1].Label != "Ship it" {
		t.Errorf("expected label %q, got %q", "Ship it", out.Subtasks[1].Label)
	}

	if !strings.Contains(gotBody, "existing_subtasks") {
		t.Errorf("request body missing existing_subtasks: %s", gotBody)
	}
	if !strings.Contains(gotBody, `"model":"test"`) {
		t.Errorf("request body missing model name: %s", gotBody)
	}

	var req struct {
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal([]byte(gotBody), &req); err != nil {
		t.Fatalf("unmarshal request body: %v", err)
	}
	if len(req.Messages) != 2 || req.Messages[0].Role != "system" || req.Messages[1].Role != "user" {
		t.Fatalf("unexpected message layout: %+v", req.Messages)
	}
	var userContent string
	if err := json.Unmarshal(req.Messages[1].Content, &userContent); err != nil {
		t.Fatalf("unmarshal user message content: %v", err)
	}
	var userPayload struct {
		ExistingSubtasks []string `json:"existing_subtasks"`
	}
	if err := json.Unmarshal([]byte(userContent), &userPayload); err != nil {
		t.Fatalf("unmarshal user payload: %v", err)
	}
	if len(userPayload.ExistingSubtasks) != 1 || userPayload.ExistingSubtasks[0] != "old" {
		t.Errorf("unexpected existing_subtasks payload: %+v", userPayload.ExistingSubtasks)
	}
}

func TestChatClientGenerateSubTasksRejectsNonJSON(t *testing.T) {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"choices":[{"message":{"content":"sorry, no json"}}]}`)
	}))
	defer srv.Close()

	client := &ChatClient{BaseURL: srv.URL, Model: "test", http: srv.Client()}

	if _, err := client.GenerateSubTasks(SubTaskGenerationInput{Title: "x"}); err == nil {
		t.Fatal("expected error for non-JSON response, got nil")
	}
}
