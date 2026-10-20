package cmd

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFetchAdapterStatusesRequiresUxEnvelope(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		want int
	}{
		{"enveloped", `{"ok":true,"data":[{"slot":"cache","mode":"real","implementation":"cache-provider"}]}`, 1},
		{"raw", `[{"slot":"cache","mode":"real","implementation":"cache-provider"}]`, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			listener, err := net.Listen("tcp4", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			port := listener.Addr().(*net.TCPAddr).Port
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/health/adapters" {
					http.NotFound(w, r)
					return
				}
				_, _ = w.Write([]byte(tc.body))
			}))
			server.Listener = listener
			server.Start()
			t.Cleanup(server.Close)

			statuses, reason := fetchAdapterStatuses(port)
			if tc.want == 1 {
				if reason != "" || len(statuses) != 1 || statuses[0].Slot != "cache" {
					t.Fatalf("statuses=%+v reason=%q", statuses, reason)
				}
			} else if statuses != nil || !strings.Contains(reason, "failed to parse gateway response") {
				t.Fatalf("raw body should be rejected, statuses=%+v reason=%q", statuses, reason)
			}
		})
	}
}
