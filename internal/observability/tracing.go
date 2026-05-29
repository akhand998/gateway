// Package observability provides Prometheus metrics and OpenTelemetry tracing.
//
// Tracing is a placeholder. To enable distributed tracing:
//   1. Add "go.opentelemetry.io/otel" and an exporter (e.g. OTLP) to go.mod.
//   2. Initialize a TracerProvider in main() and pass the tracer to middleware.
//   3. Create spans in the tenant handler with trace/span context propagation.
package observability
