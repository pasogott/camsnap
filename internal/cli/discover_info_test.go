package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/steipete/camsnap/internal/discovery"
)

func TestDiscoverInfoUsesALiveContext(t *testing.T) {
	const body = `<?xml version="1.0" encoding="UTF-8"?>
<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope">
  <s:Body>
    <tds:GetDeviceInformationResponse>
      <tds:Manufacturer>Acme</tds:Manufacturer>
      <tds:Model>Cam100</tds:Model>
      <tds:FirmwareVersion>1.2.3</tds:FirmwareVersion>
    </tds:GetDeviceInformationResponse>
  </s:Body>
</s:Envelope>`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/soap+xml")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)

	old := discoverDevices
	discoverDevices = func(ctx context.Context, _ time.Duration) ([]discovery.Device, error) {
		<-ctx.Done()
		return []discovery.Device{{Address: srv.URL, Host: "192.0.2.10:80"}}, nil
	}
	t.Cleanup(func() { discoverDevices = old })

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	root := NewRootCommand("test")
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs([]string{"discover", "--info", "--timeout", "200ms"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "Cam100") {
		t.Fatalf("stdout = %q; want model from a live info request", stdout.String())
	}
}

func TestDiscoverInfoFailureHint(t *testing.T) {
	for _, tc := range []struct {
		name, hint string
		stall      bool
	}{
		{"timeout", "retry with a longer --timeout", true},
		{"authentication", "check the ONVIF endpoint and saved camera credentials", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.stall {
					_, _ = io.Copy(io.Discard, r.Body)
					select {
					case <-r.Context().Done():
					case <-time.After(200 * time.Millisecond):
					}
					return
				}
				w.WriteHeader(http.StatusUnauthorized)
			}))
			defer srv.Close()
			old := discoverDevices
			discoverDevices = func(context.Context, time.Duration) ([]discovery.Device, error) {
				return []discovery.Device{{Address: srv.URL, Host: "192.0.2.10:80"}}, nil
			}
			defer func() { discoverDevices = old }()
			var out bytes.Buffer
			root := NewRootCommand("test")
			root.SetOut(&out)
			root.SetErr(&out)
			root.SetArgs([]string{"discover", "--info", "--timeout", "50ms"})
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out.String(), "info failed:") || !strings.Contains(out.String(), tc.hint) {
				t.Fatalf("missing recovery diagnostic: %s", out.String())
			}
		})
	}
}

func TestDiscoverHonorsCommandCancellation(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests.Add(1) }))
	defer srv.Close()
	old := discoverDevices
	discoverDevices = func(ctx context.Context, _ time.Duration) ([]discovery.Device, error) {
		if ctx.Err() == nil {
			t.Error("discovery did not inherit command cancellation")
		}
		return []discovery.Device{{Address: srv.URL, Host: "192.0.2.10:80"}}, nil
	}
	defer func() { discoverDevices = old }()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	root := NewRootCommand("test")
	root.SetContext(ctx)
	root.SetArgs([]string{"discover", "--info"})
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	if err := root.Execute(); !errors.Is(err, context.Canceled) {
		t.Fatalf("want cancellation, got %v", err)
	}
	if requests.Load() != 0 {
		t.Fatal("canceled command fetched device information")
	}
}
