// Copyright The OpenTelemetry Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

//go:build integration
// +build integration

package dockerstatsreceiver

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	testcontainers "github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/pdata/pmetric"
	rcvr "go.opentelemetry.io/collector/receiver"
	"go.opentelemetry.io/collector/receiver/receivertest"
	conventions "go.opentelemetry.io/collector/semconv/v1.6.1"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest"
)

type testHost struct {
	component.Host
	t *testing.T
}

// ReportFatalError causes the test to be run to fail.
func (h *testHost) ReportFatalError(err error) {
	h.t.Fatalf("receiver reported a fatal error: %v", err)
}

var _ component.Host = (*testHost)(nil)

func factory() (rcvr.Factory, *Config) {
	f := NewFactory()
	config := f.CreateDefaultConfig().(*Config)
	config.CollectionInterval = 1 * time.Second
	return f, config
}

func paramsAndContext(t *testing.T) (rcvr.CreateSettings, context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	logger := zaptest.NewLogger(t, zaptest.WrapOptions(zap.AddCaller()))
	settings := receivertest.NewNopCreateSettings()
	settings.Logger = logger
	return settings, ctx, cancel
}

func createNginxContainer(t *testing.T, ctx context.Context) testcontainers.Container {
	req := testcontainers.ContainerRequest{
		Image:        "docker.io/library/nginx:1.17",
		ExposedPorts: []string{"80/tcp"},
		WaitingFor:   wait.ForExposedPort(),
	}

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	require.Nil(t, err)
	require.NotNil(t, container)

	return container
}

func hasResourceScopeMetrics(containerID string, metrics []pmetric.Metrics) bool {
	for _, m := range metrics {
		for i := 0; i < m.ResourceMetrics().Len(); i++ {
			rm := m.ResourceMetrics().At(i)

			id, ok := rm.Resource().Attributes().Get(conventions.AttributeContainerID)
			if ok && id.AsString() == containerID && rm.ScopeMetrics().Len() > 0 {
				return true
			}
		}
	}
	return false
}

func TestDefaultMetricsIntegration(t *testing.T) {
	t.Parallel()
	params, ctx, cancel := paramsAndContext(t)
	defer cancel()

	container := createNginxContainer(t, ctx)

	consumer := new(consumertest.MetricsSink)
	f, config := factory()
	recv, err := f.CreateMetricsReceiver(ctx, params, config, consumer)

	require.NoError(t, err, "failed creating metrics receiver")
	require.NoError(t, recv.Start(ctx, &testHost{
		t: t,
	}))

	assert.Eventuallyf(t, func() bool {
		return hasResourceScopeMetrics(container.GetContainerID(), consumer.AllMetrics())
	}, 5*time.Second, 1*time.Second, "failed to receive any metrics")

	assert.NoError(t, recv.Shutdown(ctx))
}

func TestMonitoringAddedAndRemovedContainerIntegration(t *testing.T) {
	t.Parallel()
	params, ctx, cancel := paramsAndContext(t)
	defer cancel()
	consumer := new(consumertest.MetricsSink)
	f, config := factory()

	recv, err := f.CreateMetricsReceiver(ctx, params, config, consumer)
	require.NoError(t, err, "failed creating metrics receiver")
	require.NoError(t, recv.Start(ctx, &testHost{
		t: t,
	}))

	container := createNginxContainer(t, ctx)

	// Check metrics are collected for added container.
	assert.Eventuallyf(t, func() bool {
		return hasResourceScopeMetrics(container.GetContainerID(), consumer.AllMetrics())
	}, 5*time.Second, 1*time.Second, "failed to receive any metrics")

	container.Terminate(ctx)
	consumer.Reset()

	// Check metrics are not collected for removed container.
	assert.Never(t, func() bool {
		return hasResourceScopeMetrics(container.GetContainerID(), consumer.AllMetrics())
	}, 5*time.Second, 1*time.Second, "received undesired metrics")

	assert.NoError(t, recv.Shutdown(ctx))
}

func TestExcludedImageProducesNoMetricsIntegration(t *testing.T) {
	t.Parallel()
	params, ctx, cancel := paramsAndContext(t)
	defer cancel()

	container := createNginxContainer(t, ctx)

	f, config := factory()
	config.ExcludedImages = append(config.ExcludedImages, "*nginx*")

	consumer := new(consumertest.MetricsSink)
	recv, err := f.CreateMetricsReceiver(ctx, params, config, consumer)
	require.NoError(t, err, "failed creating metrics receiver")
	require.NoError(t, recv.Start(ctx, &testHost{
		t: t,
	}))

	assert.Never(t, func() bool {
		return hasResourceScopeMetrics(container.GetContainerID(), consumer.AllMetrics())
	}, 5*time.Second, 1*time.Second, "received undesired metrics")

	assert.NoError(t, recv.Shutdown(ctx))
}
