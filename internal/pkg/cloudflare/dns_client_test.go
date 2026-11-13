package cloudflare_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"school-erp/internal/pkg/cloudflare"
	"testing"
)

func TestCloudflareClientCreateAndDelete(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/zones/zone-123/dns_records", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.Method == http.MethodGet {
			name := r.URL.Query().Get("name")
			if name == "existing.school-erp.vn" {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"success": true,
					"result": []map[string]any{
						{"id": "rec-existing-999", "type": "CNAME", "name": name},
					},
				})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success": true,
				"result":  []any{},
			})
			return
		}
		if r.Method == http.MethodPost {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success": true,
				"result": map[string]any{
					"id":   "rec-new-101",
					"type": "CNAME",
				},
			})
			return
		}
	})

	mux.HandleFunc("/zones/zone-123/dns_records/rec-new-101", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success": true,
				"result":  map[string]any{"id": "rec-new-101"},
			})
		}
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	client := cloudflare.NewClient("test-token", "zone-123", "school-erp.vn", "tenant-ingress.school-erp.vn", true, 1)
	client.SetBaseURL(server.URL)

	ctx := context.Background()

	// 1. Idempotent: existing record returns existing ID without POST
	existingID, err := client.CreateSubdomainCNAME(ctx, "existing", "org-1")
	if err != nil || existingID != "rec-existing-999" {
		t.Fatalf("expected rec-existing-999, got %q (err=%v)", existingID, err)
	}

	// 2. New record creates via POST
	newID, err := client.CreateSubdomainCNAME(ctx, "thpt-chu-van-an", "org-2")
	if err != nil || newID != "rec-new-101" {
		t.Fatalf("expected rec-new-101, got %q (err=%v)", newID, err)
	}

	// 3. Delete record
	if err := client.DeleteRecord(ctx, newID); err != nil {
		t.Fatalf("DeleteRecord failed: %v", err)
	}
}
