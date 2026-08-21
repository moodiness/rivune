package discovery

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestLoadAcceptsReachableOriginsAndBuildsPorts(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name       string
		values     map[string]string
		wantOrigin string
		wantPort   int
		wantName   string
	}{
		{name: "https", values: map[string]string{"RIVUNE_DISCOVERY_URL": "https://media.example.com"}, wantOrigin: "https://media.example.com", wantPort: 443, wantName: "Rivune"},
		{name: "private http", values: map[string]string{"RIVUNE_DISCOVERY_URL": "http://192.168.1.20:8080/", "RIVUNE_DISCOVERY_NAME": "Living room"}, wantOrigin: "http://192.168.1.20:8080", wantPort: 8080, wantName: "Living room"},
		{name: "private ipv6", values: map[string]string{"RIVUNE_DISCOVERY_URL": "http://[fd00::20]:9090"}, wantOrigin: "http://[fd00::20]:9090", wantPort: 9090, wantName: "Rivune"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, err := Load(func(key string) (string, bool) { value, ok := test.values[key]; return value, ok })
			if err != nil {
				t.Fatal(err)
			}
			if got.Origin != test.wantOrigin || got.Port != test.wantPort || got.InstanceName != test.wantName {
				t.Fatalf("Load() = %#v", got)
			}
		})
	}
}

func TestLoadRejectsUnsafeOrUnusableOrigins(t *testing.T) {
	t.Parallel()
	for _, value := range []string{
		"", "http://localhost:8080", "http://127.0.0.1:8080", "http://[::1]:8080",
		"http://media.example.com", "http://198.51.100.20:8080", "ftp://media.example.com",
		"https://user:secret@media.example.com", "https://media.example.com/path", "https://media.example.com?token=secret",
		"https://[fe80::1]:8080", "https://0.0.0.0:8080",
	} {
		value := value
		t.Run(strings.ReplaceAll(value, "/", "_"), func(t *testing.T) {
			t.Parallel()
			_, err := Load(func(key string) (string, bool) { return value, key == "RIVUNE_DISCOVERY_URL" })
			if err == nil {
				t.Fatalf("Load accepted %q", value)
			}
		})
	}
}

func TestRunPublishesContractUntilCancellation(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	registered := make(chan struct{})
	shutdown := make(chan struct{})
	result := make(chan error, 1)
	var gotInstance, gotService, gotDomain string
	var gotPort int
	var gotText []string
	go func() {
		result <- run(ctx, slog.New(slog.NewTextHandler(io.Discard, nil)), Config{
			InstanceName: "Living room",
			Origin:       "https://media.example.com",
			Port:         443,
		}, "1.10.0", func(instance, service, domain string, port int, text []string, _ []net.Interface) (func(), error) {
			gotInstance, gotService, gotDomain, gotPort = instance, service, domain, port
			gotText = append([]string(nil), text...)
			close(registered)
			return func() { close(shutdown) }, nil
		})
	}()
	select {
	case <-registered:
	case <-time.After(time.Second):
		t.Fatal("service was not registered")
	}
	if gotInstance != "Living room" || gotService != "_rivune._tcp" || gotDomain != "local." || gotPort != 443 {
		t.Fatalf("unexpected registration: %q %q %q %d", gotInstance, gotService, gotDomain, gotPort)
	}
	if want := []string{"url=https://media.example.com", "protocol=20", "version=1.10.0"}; !reflect.DeepEqual(gotText, want) {
		t.Fatalf("TXT records = %#v, want %#v", gotText, want)
	}
	cancel()
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run did not stop after cancellation")
	}
	select {
	case <-shutdown:
	default:
		t.Fatal("registered service was not shut down")
	}
}

func TestRunReportsRegistrationFailure(t *testing.T) {
	t.Parallel()
	registrationError := errors.New("no multicast interface")
	err := run(context.Background(), slog.New(slog.NewTextHandler(io.Discard, nil)), Config{
		InstanceName: "Rivune", Origin: "https://media.example.com", Port: 443,
	}, "1.10.0", func(string, string, string, int, []string, []net.Interface) (func(), error) {
		return nil, registrationError
	})
	if !errors.Is(err, registrationError) {
		t.Fatalf("run error = %v", err)
	}
}
