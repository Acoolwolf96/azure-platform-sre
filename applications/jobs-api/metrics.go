package main

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	jobsAPIHTTPRequests = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "jobs_api_http_requests_total",
			Help: "Total HTTP requests handled by jobs-api.",
		},
		[]string{"method", "path", "status"},
	)

	jobsAPIHTTPDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "jobs_api_http_request_duration_seconds",
			Help:    "HTTP request duration for jobs-api.",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"method", "path"},
	)

	jobsAPISubmitted = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "jobs_api_jobs_submitted_total",
			Help: "Total jobs successfully submitted to Service Bus.",
		},
	)

	jobsAPIErrors = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "jobs_api_errors_total",
			Help: "Total jobs-api internal errors by operation.",
		},
		[]string{"operation"},
	)
)

func init() {
	prometheus.MustRegister(
		jobsAPIHTTPRequests,
		jobsAPIHTTPDuration,
		jobsAPISubmitted,
		jobsAPIErrors,
	)
}

func registerMetrics(mux *http.ServeMux) {
	mux.Handle("/metrics", promhttp.Handler())
}

type metricsResponseWriter struct {
	http.ResponseWriter
	status int
}

func (w *metricsResponseWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *metricsResponseWriter) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(data)
}

func withHTTPMetrics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/metrics" {
			next.ServeHTTP(w, r)
			return
		}

		started := time.Now()
		recorder := &metricsResponseWriter{ResponseWriter: w}

		next.ServeHTTP(recorder, r)

		status := recorder.status
		if status == 0 {
			status = http.StatusOK
		}

		path := metricPath(r.URL.Path)

		jobsAPIHTTPRequests.
			WithLabelValues(r.Method, path, strconv.Itoa(status)).
			Inc()

		jobsAPIHTTPDuration.
			WithLabelValues(r.Method, path).
			Observe(time.Since(started).Seconds())
	})
}

func metricPath(path string) string {
	if strings.HasPrefix(path, "/jobs/") {
		return "/jobs/{id}"
	}
	return path
}
