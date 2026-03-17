// Package otel provides OpenTelemetry metric helpers for marvel.
package otel

import (
	"context"

	"go.opentelemetry.io/otel/exporters/stdout/stdoutmetric"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
)

// NewStdoutMeterProvider creates a meter provider that exports to stdout.
func NewStdoutMeterProvider() (*sdkmetric.MeterProvider, error) {
	exporter, err := stdoutmetric.New()
	if err != nil {
		return nil, err
	}
	reader := sdkmetric.NewPeriodicReader(exporter)
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	return provider, nil
}

// NewContextGauge creates the marvel.agent.context_window_percent gauge.
func NewContextGauge(meter metric.Meter) (metric.Float64Gauge, error) {
	return meter.Float64Gauge("marvel.agent.context_window_percent",
		metric.WithDescription("Agent context window usage as a percentage"),
		metric.WithUnit("%"),
	)
}

// Shutdown gracefully shuts down the meter provider.
func Shutdown(ctx context.Context, provider *sdkmetric.MeterProvider) error {
	return provider.Shutdown(ctx)
}
