// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

package live

import (
	"github.com/prometheus/client_golang/prometheus"
)

type hubMetrics struct {
	subscriptions prometheus.Gauge
	streams       prometheus.Gauge
	messages      prometheus.Counter
	deliveries    prometheus.Counter
	dropped       prometheus.Counter
	resyncs       *prometheus.CounterVec
	connected     prometheus.Gauge
	reconnects    prometheus.Counter
	lag           prometheus.Histogram
}

func newHubMetrics(reg prometheus.Registerer) *hubMetrics {
	m := &hubMetrics{
		subscriptions: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "cms_live_subscriptions",
			Help: "Local subscribers of live events (open event streams included).",
		}),
		streams: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "cms_live_streams",
			Help: "Open server-sent event streams.",
		}),
		messages: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "cms_live_messages_total",
			Help: "Live events received by the hub (from Valkey or published locally).",
		}),
		deliveries: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "cms_live_deliveries_total",
			Help: "Live events handed to local subscribers.",
		}),
		dropped: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "cms_live_dropped_total",
			Help: "Live events dropped because a subscriber's buffer was full (it gets a resync).",
		}),
		resyncs: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "cms_live_resyncs_total",
			Help: "Resync markers handed to subscribers, by reason (overflow, reconnect, gap).",
		}, []string{"reason"}),
		connected: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "cms_live_connected",
			Help: "1 while the hub receives live events (its Valkey subscription is up).",
		}),
		reconnects: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "cms_live_reconnects_total",
			Help: "Times the hub lost its Valkey subscription.",
		}),
		lag: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "cms_live_lag_seconds",
			Help:    "Time from Publish to the hub receiving the event (clocks of both hosts).",
			Buckets: []float64{.0005, .001, .0025, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5},
		}),
	}
	for _, r := range []string{ReasonOverflow, ReasonReconnect, ReasonGap} {
		m.resyncs.WithLabelValues(r)
	}
	if reg != nil {
		reg.MustRegister(m.subscriptions, m.streams, m.messages, m.deliveries, m.dropped,
			m.resyncs, m.connected, m.reconnects, m.lag)
	}
	return m
}

type pubMetrics struct {
	published *prometheus.CounterVec
	errors    prometheus.Counter
	dropped   prometheus.Counter
}

func newPubMetrics(reg prometheus.Registerer, queueLen func() float64) *pubMetrics {
	m := &pubMetrics{
		published: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "cms_live_published_total",
			Help: "Live events published to Valkey, by event name (one per topic).",
		}, []string{"event"}),
		errors: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "cms_live_publish_errors_total",
			Help: "Live events lost because Valkey could not be reached (one per topic).",
		}),
		dropped: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "cms_live_publish_dropped_total",
			Help: "Live events dropped by Publish because the queue was full or the event invalid.",
		}),
	}
	if reg != nil {
		reg.MustRegister(m.published, m.errors, m.dropped,
			prometheus.NewGaugeFunc(prometheus.GaugeOpts{
				Name: "cms_live_publish_queue",
				Help: "Live events waiting to be sent to Valkey.",
			}, queueLen))
	}
	return m
}
