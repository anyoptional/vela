package eurekaadapter

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	eureka "github.com/anyoptional/eureka-go"
)

func TestLive(t *testing.T) {
	serverURL := os.Getenv("vela_EUREKA_URL")
	if serverURL == "" {
		t.Skip("vela_EUREKA_URL is not set")
	}

	id := fmt.Sprintf("%d-%d", os.Getpid(), time.Now().UnixNano())
	serviceID := "vela-LIVE-" + id
	cfg := eureka.DefaultConfig()
	cfg.Client.Zones = []eureka.ZoneURLs{{Zone: "default", URLs: []string{serverURL}}}
	cfg.Client.Zone = "default"
	cfg.Client.PollInterval = time.Second
	cfg.Instance.App = serviceID
	cfg.Instance.InstanceId = id
	cfg.Instance.HostName = "vela-live.local"
	cfg.Instance.IPAddr = "127.0.0.1"
	cfg.Instance.Port = 8080
	cfg.Instance.Status = eureka.UP

	client, err := eureka.New(cfg)
	if err != nil {
		t.Fatalf("eureka.New(): %v", err)
	}
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Errorf("client.Close(): %v", err)
		}
	})
	discovery := New(client)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := client.Start(ctx); err != nil {
		t.Fatalf("client.Start(): %v", err)
	}

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		instances, err := discovery.Instances(ctx, serviceID)
		if err != nil {
			t.Fatalf("Instances(): %v", err)
		}
		for _, instance := range instances {
			if instance.ID == id && instance.ServiceID == serviceID {
				return
			}
		}
		select {
		case <-ctx.Done():
			t.Fatalf("registered instance %q was not discovered", id)
		case <-ticker.C:
		}
	}
}
