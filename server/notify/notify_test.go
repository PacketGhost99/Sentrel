package notify

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"heartbeat/server/netguard"
)

func msg() Message {
	return Message{Subject: "[CRITICAL] disk on web-1", Status: "firing", Severity: "critical", Product: "Heartbeat",
		Items: []Item{{AlertID: 1, Status: "firing", Severity: "critical", Title: `disk "/" on web-1`, Message: "95%", Host: "web-1"}}}
}

func capture(t *testing.T) (*httptest.Server, *[]byte, *http.Header) {
	var body []byte
	var hdr http.Header
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		hdr = r.Header.Clone()
	}))
	t.Cleanup(s.Close)
	return s, &body, &hdr
}

func TestValidate(t *testing.T) {
	ok := map[string]string{
		"discord":  `{"url":"https://discord.com/api/webhooks/1/x"}`,
		"telegram": `{"bot_token":"1:abc","chat_id":"42"}`,
		"ntfy":     `{"topic":"alerts"}`,
		"email":    `{"to":["a@b.c"]}`,
		"webhook":  `{"url":"https://h.example/x","template":"{{.Subject}}"}`,
	}
	for typ, cfg := range ok {
		if err := Validate(typ, json.RawMessage(cfg)); err != nil {
			t.Errorf("%s: %v", typ, err)
		}
	}
	bad := map[string]string{
		"discord": `{"url":"ftp://x"}`, "telegram": `{"bot_token":"x"}`, "pushover": `{}`, "webhook": `{"url":"https://x","template":"{{.Nope"}`, "sms": `{}`,
	}
	for typ, cfg := range bad {
		if err := Validate(typ, json.RawMessage(cfg)); err == nil {
			t.Errorf("%s accepted %s", typ, cfg)
		}
	}
}

func TestWebhookDefaultAndTemplate(t *testing.T) {
	s, body, _ := capture(t)
	if err := Send(context.Background(), netguard.Allow, "webhook", json.RawMessage(`{"url":"`+s.URL+`"}`), SMTP{}, msg()); err != nil {
		t.Fatal(err)
	}
	var m Message
	if json.Unmarshal(*body, &m) != nil || len(m.Items) != 1 || m.Items[0].Host != "web-1" {
		t.Fatalf("default payload: %s", *body)
	}
	cfg := `{"url":"` + s.URL + `","template":"{\"text\": {{json .Subject}}, \"n\": {{len .Items}}}"}`
	if err := Send(context.Background(), netguard.Allow, "webhook", json.RawMessage(cfg), SMTP{}, msg()); err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(*body, &out); err != nil || out["text"] != "[CRITICAL] disk on web-1" || out["n"] != 1.0 {
		t.Fatalf("template output %s (%v)", *body, err)
	}
}

func TestPresetsShape(t *testing.T) {
	s, body, hdr := capture(t)
	if err := Send(context.Background(), netguard.Allow, "discord", json.RawMessage(`{"url":"`+s.URL+`"}`), SMTP{}, msg()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(*body), `"embeds"`) {
		t.Errorf("discord: %s", *body)
	}
	if err := Send(context.Background(), netguard.Allow, "teams", json.RawMessage(`{"url":"`+s.URL+`"}`), SMTP{}, msg()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(*body), "AdaptiveCard") {
		t.Errorf("teams: %s", *body)
	}
	if err := Send(context.Background(), netguard.Allow, "ntfy", json.RawMessage(`{"server":"`+s.URL+`","topic":"ops","token":"tk"}`), SMTP{}, msg()); err != nil {
		t.Fatal(err)
	}
	if (*hdr).Get("Priority") != "5" || (*hdr).Get("Authorization") != "Bearer tk" {
		t.Errorf("ntfy headers: %v", *hdr)
	}
	// Errors from the endpoint surface.
	fail := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "nope", 400) }))
	defer fail.Close()
	if err := Send(context.Background(), netguard.Allow, "slack", json.RawMessage(`{"url":"`+fail.URL+`"}`), SMTP{}, msg()); err == nil || !strings.Contains(err.Error(), "400") {
		t.Errorf("expected HTTP 400 error, got %v", err)
	}
}

func TestTenantChannelsBlockedFromPrivateTargets(t *testing.T) {
	s, _, _ := capture(t) // listens on 127.0.0.1
	cfgs := map[string]string{
		"webhook": `{"url":"` + s.URL + `"}`,
		"slack":   `{"url":"` + s.URL + `"}`,
		"teams":   `{"url":"` + s.URL + `"}`,
		"discord": `{"url":"` + s.URL + `"}`,
		"ntfy":    `{"server":"` + s.URL + `","topic":"ops"}`,
		"gotify":  `{"server":"` + s.URL + `","token":"tk"}`,
	}
	for typ, cfg := range cfgs {
		err := Send(context.Background(), netguard.Strict, typ, json.RawMessage(cfg), SMTP{}, msg())
		if err == nil || !strings.Contains(err.Error(), "not allowed") {
			t.Errorf("%s: tenant send to loopback should be blocked, got %v", typ, err)
		}
		if err := Send(context.Background(), netguard.Allow, typ, json.RawMessage(cfg), SMTP{}, msg()); err != nil {
			t.Errorf("%s: provider send to loopback failed: %v", typ, err)
		}
	}
}
