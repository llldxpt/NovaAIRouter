package metrics

import (
	"sync"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/rs/zerolog"
)

type Metrics struct {
	log                  zerolog.Logger
	requestTotal         *prometheus.CounterVec
	requestDuration      *prometheus.HistogramVec
	activeRequests       *prometheus.GaugeVec
	queueLength          *prometheus.GaugeVec
	endpointHealthy      *prometheus.GaugeVec
	proxyErrors          *prometheus.CounterVec
	clusterNodes         prometheus.Gauge
	clusterEndpoints     prometheus.Gauge
	circuitBreakerState  *prometheus.GaugeVec
	circuitBreakerTrips  *prometheus.CounterVec
	mu                   sync.Mutex
}

var (
	once     sync.Once
	metricsInstance *Metrics
)

func New() *Metrics {
	once.Do(func() {
		metricsInstance = &Metrics{
			log: zerolog.Nop(),
		}

		zerolog.TimeFieldFormat = "2006-01-02T15:04:05.999Z07:00"

		metricsInstance.requestTotal = promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "gateway_requests_total",
				Help: "Total number of requests",
			},
			[]string{"path", "node", "status"},
		)

		metricsInstance.requestDuration = promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "gateway_request_duration_seconds",
				Help:    "Request duration in seconds",
				Buckets: prometheus.DefBuckets,
			},
			[]string{"path", "node"},
		)

		metricsInstance.activeRequests = promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "gateway_active_requests",
				Help: "Number of active requests",
			},
			[]string{"path", "node"},
		)

		metricsInstance.queueLength = promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "gateway_queue_length",
				Help: "Number of queued requests",
			},
			[]string{"path", "node"},
		)

		metricsInstance.endpointHealthy = promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "gateway_endpoint_healthy",
				Help: "Endpoint health status (0 or 1)",
			},
			[]string{"path", "node"},
		)

		metricsInstance.proxyErrors = promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "gateway_proxy_errors_total",
				Help: "Total number of proxy errors",
			},
			[]string{"type", "node"},
		)

		metricsInstance.clusterNodes = promauto.NewGauge(
			prometheus.GaugeOpts{
				Name: "gateway_cluster_nodes",
				Help: "Number of nodes in the cluster",
			},
		)

		metricsInstance.clusterEndpoints = promauto.NewGauge(
			prometheus.GaugeOpts{
				Name: "gateway_cluster_endpoints",
				Help: "Number of endpoints in the cluster",
			},
		)

		metricsInstance.circuitBreakerState = promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "gateway_circuit_breaker_state",
				Help: "Circuit breaker state (0=closed, 1=open, 2=halfopen)",
			},
			[]string{"path", "node"},
		)

		metricsInstance.circuitBreakerTrips = promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "gateway_circuit_breaker_trips_total",
				Help: "Total number of circuit breaker trips",
			},
			[]string{"path", "node"},
		)
	})
	return metricsInstance
}

func (m *Metrics) SetLogger(log zerolog.Logger) {
	m.log = log
}

func (m *Metrics) IncRequestTotal(path, node, status string) {
	m.requestTotal.WithLabelValues(path, node, status).Inc()
}

func (m *Metrics) ObserveRequestDuration(path, node string, duration float64) {
	m.requestDuration.WithLabelValues(path, node).Observe(duration)
}

func (m *Metrics) SetActiveRequests(path, node string, value float64) {
	m.activeRequests.WithLabelValues(path, node).Set(value)
}

func (m *Metrics) SetQueueLength(path, node string, value float64) {
	m.queueLength.WithLabelValues(path, node).Set(value)
}

func (m *Metrics) SetEndpointHealthy(path, node string, value float64) {
	m.endpointHealthy.WithLabelValues(path, node).Set(value)
}

func (m *Metrics) IncProxyErrors(errorType, node string) {
	m.proxyErrors.WithLabelValues(errorType, node).Inc()
}

func (m *Metrics) SetClusterNodes(count float64) {
	m.clusterNodes.Set(count)
}

func (m *Metrics) SetClusterEndpoints(count float64) {
	m.clusterEndpoints.Set(count)
}

func (m *Metrics) SetCircuitBreakerState(path, node string, state float64) {
	m.circuitBreakerState.WithLabelValues(path, node).Set(state)
}

func (m *Metrics) IncCircuitBreakerTrips(path, node string) {
	m.circuitBreakerTrips.WithLabelValues(path, node).Inc()
}

// Shutdown 关闭 metrics 服务器（无操作，因为 metrics 通过 Admin API 暴露）
func (m *Metrics) Shutdown() {
	// no-op: metrics 通过 Admin API 的 /metrics 端点暴露，无需独立服务器
}
