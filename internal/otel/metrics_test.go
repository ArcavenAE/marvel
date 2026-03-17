package otel

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

func TestNewStdoutMeterProvider(t *testing.T) {
	t.Parallel()
	provider, err := NewStdoutMeterProvider()
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}
	t.Cleanup(func() {
		_ = Shutdown(context.Background(), provider)
	})

	meter := provider.Meter("marvel-test")
	gauge, err := NewContextGauge(meter)
	if err != nil {
		t.Fatalf("create gauge: %v", err)
	}

	// Recording should not panic.
	gauge.Record(context.Background(), 42.5,
		metric.WithAttributes(
			attribute.String("workspace", "test"),
			attribute.String("team", "agents"),
			attribute.String("session", "agent-0"),
		),
	)
}
