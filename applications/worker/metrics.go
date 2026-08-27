package main

import (
	"fmt"
	"log"
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	workerJobsReceived = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "worker_jobs_received_total",
			Help: "Total jobs received from Service Bus.",
		},
	)

	workerJobsCompleted = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "worker_jobs_completed_total",
			Help: "Total jobs successfully completed.",
		},
	)

	workerJobsFailed = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "worker_jobs_failed_total",
			Help: "Total job failures by processing stage.",
		},
		[]string{"stage"},
	)

	workerJobDuration = prometheus.NewHistogram(
		prometheus.HistogramOpts{
			Name:    "worker_job_processing_duration_seconds",
			Help:    "Time spent processing jobs.",
			Buckets: prometheus.DefBuckets,
		},
	)

	workerReady = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "worker_ready",
			Help: "Whether the worker is connected and ready to consume jobs.",
		},
	)
)

func init() {
	prometheus.MustRegister(
		workerJobsReceived,
		workerJobsCompleted,
		workerJobsFailed,
		workerJobDuration,
		workerReady,
	)
}

func startWorkerMetricsServer() {
	port := getenv("METRICS_PORT", "9090")

	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())

	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintln(w, "ok")
	})

	go func() {
		log.Printf("worker metrics listening on :%s", port)

		if err := http.ListenAndServe(":"+port, mux); err != nil {
			log.Printf("worker metrics server error: %v", err)
		}
	}()
}
