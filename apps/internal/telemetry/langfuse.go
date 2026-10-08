// Package telemetry 通过 OpenTelemetry 端点配置可选的 Langfuse 链路追踪。
package telemetry

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.37.0"
	"go.opentelemetry.io/otel/trace"
)

const (
	defaultHost    = "https://cloud.langfuse.com"
	otlpTracesPath = "/api/public/otel/v1/traces"
)

// Config 包含 Langfuse 项目凭据。当公钥和私钥均为空时，链路追踪将被禁用。
type Config struct {
	PublicKey string
	SecretKey string
	Host      string
}

// Langfuse 持有链路追踪提供器。进程退出前必须将其关闭，以发送已缓冲的 span。
type Langfuse struct {
	provider *sdktrace.TracerProvider
	tracer   trace.Tracer
}

// New 创建 Langfuse OTLP/HTTP 导出器。这里特意不对提示词、响应和工具载荷启用过滤，
// MiniCode 将上传完整的观测数据。
func New(ctx context.Context, cfg Config) (*Langfuse, error) {
	publicKey := strings.TrimSpace(cfg.PublicKey)
	secretKey := strings.TrimSpace(cfg.SecretKey)
	if publicKey == "" && secretKey == "" {
		return &Langfuse{tracer: trace.NewNoopTracerProvider().Tracer("minicode")}, nil
	}
	if publicKey == "" || secretKey == "" {
		return nil, errors.New("langfuse: public_key and secret_key must both be set")
	}
	host := strings.TrimRight(strings.TrimSpace(cfg.Host), "/")
	if host == "" {
		host = defaultHost
	}
	endpoint := host + otlpTracesPath
	auth := base64.StdEncoding.EncodeToString([]byte(publicKey + ":" + secretKey))
	exporter, err := otlptracehttp.New(ctx,
		otlptracehttp.WithEndpointURL(endpoint),
		otlptracehttp.WithHeaders(map[string]string{"Authorization": "Basic " + auth}),
	)
	if err != nil {
		return nil, fmt.Errorf("langfuse: create exporter: %w", err)
	}
	res, err := resource.New(ctx, resource.WithAttributes(semconv.ServiceName("minicode")))
	if err != nil {
		return nil, fmt.Errorf("langfuse: create resource: %w", err)
	}
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithResource(res),
		sdktrace.WithBatcher(exporter, sdktrace.WithBatchTimeout(time.Second)),
	)
	return &Langfuse{provider: provider, tracer: provider.Tracer("github.com/MiniCode-go/minicode")}, nil
}

// Tracer 返回已配置的 Langfuse 链路追踪器；未配置时返回空操作追踪器。
func (l *Langfuse) Tracer() trace.Tracer {
	if l == nil || l.tracer == nil {
		return trace.NewNoopTracerProvider().Tracer("minicode")
	}
	return l.tracer
}

// Close 刷新并发送所有已缓冲的观测数据。
func (l *Langfuse) Close(ctx context.Context) error {
	if l == nil || l.provider == nil {
		return nil
	}
	if err := l.provider.Shutdown(ctx); err != nil {
		return fmt.Errorf("langfuse: flush traces: %w", err)
	}
	return nil
}
