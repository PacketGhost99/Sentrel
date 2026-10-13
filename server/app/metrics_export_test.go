package app

import (
	"encoding/csv"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"sentrel/server/metrics"
)

func TestMetricsExport(t *testing.T) {
	e := newTestEnv(t)
	admin := e.setupAdmin()
	_, ten, _ := admin.do("POST", "/tenants", map[string]any{"name": "Acme Ltd"})
	tid := ten["id"].(float64)
	if code, _, raw := admin.do("POST", "/users", map[string]any{"username": "acme-admin", "password": testPassword, "role": "tenant_admin", "tenant_id": tid}); code != 201 {
		t.Fatalf("user: %d %s", code, raw)
	}
	_, h, _ := admin.do("POST", "/hosts/target", map[string]any{"name": "web 1", "address": "192.0.2.1", "tenant_id": 0})
	hid := int64(h["id"].(float64))
	now := time.Now()
	var samples []metrics.Sample
	for i := 0; i < 5; i++ {
		ts := now.Add(-time.Duration(5-i) * time.Minute).UnixMilli()
		samples = append(samples,
			metrics.Sample{Name: "cpu_usage_percent", Value: float64(10 + i), TsMs: ts},
			metrics.Sample{Name: "disk_used_percent", Labels: map[string]string{"mount": "/"}, Value: 50, TsMs: ts},
		)
	}
	if err := e.a.ts.Append(hid, 0, samples); err != nil {
		t.Fatal(err)
	}
	base := "/hosts/" + itoa(hid) + "/metrics/export"

	// CSV shape.
	code, _, raw := admin.do("GET", base+"?format=csv&range=900&step=60", nil)
	if code != 200 {
		t.Fatalf("csv: %d %s", code, raw)
	}
	recs, err := csv.NewReader(strings.NewReader(string(raw))).ReadAll()
	if err != nil || len(recs) < 2 {
		t.Fatalf("csv parse: %v %q", err, raw)
	}
	hdr := strings.Join(recs[0], "|")
	if recs[0][0] != "timestamp" || recs[0][1] != "unix" || !strings.Contains(hdr, "cpu_usage_percent") || !strings.Contains(hdr, "disk_used_percent{mount=/}") {
		t.Fatalf("csv header: %v", recs[0])
	}
	if _, err := time.Parse(time.RFC3339, recs[1][0]); err != nil || !strings.HasSuffix(recs[1][0], "Z") {
		t.Fatalf("timestamp not ISO UTC: %q", recs[1][0])
	}
	for _, r := range recs {
		if len(r) != len(recs[0]) {
			t.Fatalf("ragged row: %v", r)
		}
	}

	// JSON shape and headers.
	code, m, raw := admin.do("GET", base+"?format=json&range=900&step=60", nil)
	if code != 200 || m["host"] != "web 1" || m["step"].(float64) != 60 {
		t.Fatalf("json: %d %s", code, raw)
	}
	var doc struct {
		Series []struct {
			Name   string            `json:"name"`
			Labels map[string]string `json:"labels"`
			Unit   string            `json:"unit"`
			Points [][2]float64      `json:"points"`
		} `json:"series"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil || len(doc.Series) < 2 {
		t.Fatalf("json series: %v %s", err, raw)
	}
	found := false
	for _, s := range doc.Series {
		if s.Name == "disk_used_percent{mount=/}" && s.Labels["mount"] == "/" && s.Unit == "percent" && len(s.Points) > 0 {
			found = true
		}
	}
	if !found {
		t.Fatalf("disk series missing: %s", raw)
	}

	// Row cap: 30 days at 1s step is far over the limit.
	code, m, raw = admin.do("GET", base+"?range=2592000&step=1", nil)
	if code != 400 || !strings.Contains(string(raw), "coarser step") {
		t.Fatalf("cap: %d %s", code, raw)
	}
	if code, _, _ := admin.do("GET", base+"?format=xml", nil); code != 400 {
		t.Fatalf("bad format: %d", code)
	}

	// Tenancy: another tenant cannot export a provider host.
	acme := e.login("acme-admin", testPassword)
	if code, _, _ := acme.do("GET", base+"?format=csv&range=900", nil); code != 404 {
		t.Fatalf("cross-tenant export: %d", code)
	}
}
