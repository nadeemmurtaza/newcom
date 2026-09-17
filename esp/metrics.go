package main

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	MetricEmailsReceived = promauto.NewCounter(prometheus.CounterOpts{
		Name: "esp_emails_received_total",
		Help: "The total number of received inbound emails",
	})
	MetricEmailsSent = promauto.NewCounter(prometheus.CounterOpts{
		Name: "esp_emails_sent_total",
		Help: "The total number of outbound emails sent via MTA",
	})
	MetricEmailsRejected = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "esp_emails_rejected_total",
		Help: "The total number of rejected inbound emails by reason",
	}, []string{"reason"})
	MetricWebhooksDispatched = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "esp_webhooks_dispatched_total",
		Help: "The total number of webhooks dispatched",
	}, []string{"status"})
)
