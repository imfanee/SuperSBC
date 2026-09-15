// Package metrics defines the Prometheus metrics of Section 7 (monitoring).
// ASR, ACD and NER per customer or carrier are derived in Grafana from the
// counters here (see deploy/grafana).
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	CallsInProgress = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: "sbc", Name: "calls_in_progress", Help: "Calls with an open reservation (active_calls rows).",
	})
	Setups = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "sbc", Name: "call_setups_total", Help: "Setup decisions by customer and outcome (dial or the reject step).",
	}, []string{"customer", "outcome"})
	Rejections = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "sbc", Name: "call_rejections_total", Help: "Rejections by step and SIP code.",
	}, []string{"step", "code"})
	ReservationFailures = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "sbc", Name: "reservation_failures_total", Help: "Reservations refused for lack of funds, by customer.",
	}, []string{"customer"})
	Attempts = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "sbc", Name: "carrier_attempts_total", Help: "Bridge attempts by carrier and classification.",
	}, []string{"carrier", "classification"})
	AttemptSIPCodes = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "sbc", Name: "carrier_attempt_sip_codes_total", Help: "Final SIP codes received from carriers.",
	}, []string{"carrier", "code"})
	PDD = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "sbc", Name: "pdd_seconds", Help: "Post dial delay per carrier.",
		Buckets: []float64{0.1, 0.25, 0.5, 1, 2, 3, 5, 8, 12, 20},
	}, []string{"carrier"})
	Billed = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "sbc", Name: "calls_billed_total", Help: "Finished calls by customer, carrier and disposition.",
	}, []string{"customer", "carrier", "disposition"})
	Billsec = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "sbc", Name: "billsec_total", Help: "Answered seconds by customer and carrier.",
	}, []string{"customer", "carrier"})
	Revenue = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "sbc", Name: "revenue_total", Help: "Sell price of billed calls by customer (currency units).",
	}, []string{"customer"})
	Cost = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "sbc", Name: "cost_total", Help: "Carrier cost of billed calls by carrier (currency units).",
	}, []string{"carrier"})
	InternalAPIDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "sbc", Name: "internal_api_duration_seconds", Help: "Latency of the Lua to API calls.",
		Buckets: []float64{0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2},
	}, []string{"path", "status"})
	GatewayUp = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "sbc", Name: "gateway_up", Help: "1 when the carrier gateway is UP (OPTIONS ping), 0 when DOWN.",
	}, []string{"gateway"})
	CarrierDegraded = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "sbc", Name: "carrier_degraded", Help: "1 while the circuit breaker holds the carrier degraded.",
	}, []string{"carrier"})
	CPS = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "sbc", Name: "cps", Help: "Call setups in the last second, by scope (system, customer:<name>).",
	}, []string{"scope"})
	// STIR counts STIR/SHAKEN verification outcomes by status (D-64).
	STIR = promauto.NewCounterVec(prometheus.CounterOpts{Namespace: "sbc", Name: "stir_verifications_total", Help: "STIR/SHAKEN verifications by status"}, []string{"status"})
	Bans = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "sbc", Name: "bans_total", Help: "Source addresses banned by the scanner protection (auto) or by an operator (manual).",
	}, []string{"kind"})
	MediaMode = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "sbc", Name: "media_mode_total", Help: "Answered calls by media mode (relay or transcode).",
	}, []string{"mode"})
)
