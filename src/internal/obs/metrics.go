package obs

import "github.com/prometheus/client_golang/prometheus"

// Prometheus metrics exposed by stAirCase (CHECK 10.2.1).
// Labels are bounded-cardinality only — no run_id, user_id, or UUID-typed values (CHECK 10.2.2).
var (
	// YieldsTotal counts yield_request outcomes by action type and operator decision.
	YieldsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "staircase_yields_total",
		Help: "Total yield requests by action type and outcome.",
	}, []string{"action_type", "outcome"}) // outcome: approved | rejected

	// YieldLatency measures the time from yield_request receipt to yield_response send.
	YieldLatency = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "staircase_yield_latency",
		Help:    "Seconds from yield_request receipt to yield_response send.",
		Buckets: prometheus.DefBuckets,
	}, []string{"action_type"})

	// SecretAccessTotal counts secret fetch attempts by outcome.
	SecretAccessTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "staircase_secret_access_total",
		Help: "Total secret access attempts by outcome.",
	}, []string{"outcome"}) // outcome: success | error | not_found

	// IPCMessagesTotal counts all IPC messages received by kind.
	IPCMessagesTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "staircase_ipc_messages_total",
		Help: "Total IPC messages received by kind.",
	}, []string{"kind"})

	// RunDuration measures the wall-clock time of a completed run.
	RunDuration = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "staircase_run_duration",
		Help:    "Seconds from run start to completion.",
		Buckets: []float64{5, 15, 30, 60, 120, 300, 600, 1800},
	})

	// ActiveRuns tracks the number of runs currently in the agent loop.
	ActiveRuns = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "staircase_active_runs",
		Help: "Number of runs currently executing in the agent loop.",
	})

	// AuditChainLength tracks the cumulative number of audit event log entries appended.
	AuditChainLength = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "staircase_audit_chain_length",
		Help: "Cumulative audit event log entries appended this process lifetime.",
	})
)

func init() {
	prometheus.MustRegister(
		YieldsTotal,
		YieldLatency,
		SecretAccessTotal,
		IPCMessagesTotal,
		RunDuration,
		ActiveRuns,
		AuditChainLength,
	)
}
