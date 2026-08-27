package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	amqp "github.com/Azure/go-amqp"
	_ "github.com/jackc/pgx/v5/stdlib"
)

type jobRequest struct {
	Payload string `json:"payload"`
}

type jobMessage struct {
	ID        string    `json:"id"`
	Payload   string    `json:"payload"`
	CreatedAt time.Time `json:"created_at"`
}

type jobRecord struct {
	ID         string    `json:"id"`
	Payload    string    `json:"payload"`
	Status     string    `json:"status"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
	ResultBlob *string   `json:"result_blob"`
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func newJobID() string {
	b := make([]byte, 8)

	if _, err := rand.Read(b); err != nil {
		return time.Now().UTC().Format("20060102150405")
	}

	return hex.EncodeToString(b)
}

func publishJob(ctx context.Context, brokerURL, queue string, job jobMessage) error {
	conn, err := amqp.Dial(ctx, brokerURL, &amqp.ConnOptions{
		SASLType: amqp.SASLTypeAnonymous(),
	})
	if err != nil {
		return err
	}
	defer conn.Close()

	session, err := conn.NewSession(ctx, nil)
	if err != nil {
		return err
	}
	defer session.Close(ctx)

	sender, err := session.NewSender(ctx, "/"+queue, nil)
	if err != nil {
		return err
	}
	defer sender.Close(ctx)

	body, err := json.Marshal(job)
	if err != nil {
		return err
	}

	return sender.Send(ctx, amqp.NewMessage(body), nil)
}

func main() {
	port := getenv("PORT", "8080")

	brokerURL := getenv("SERVICEBUS_URL", "amqp://servicebus:5672")
	queue := getenv("SERVICEBUS_QUEUE", "jobs")

	pgHost := getenv("PGHOST", "postgres")
	pgPort := getenv("PGPORT", "5432")
	pgUser := os.Getenv("PGUSER")
	pgPassword := os.Getenv("PGPASSWORD")
	pgDatabase := getenv("PGDATABASE", "jobsdb")
	pgSSLMode := getenv("PGSSLMODE", "disable")

	dsn := fmt.Sprintf(
		"host=%s port=%s user=%s password=%s dbname=%s sslmode=%s",
		pgHost,
		pgPort,
		pgUser,
		pgPassword,
		pgDatabase,
		pgSSLMode,
	)

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		log.Fatalf("failed to configure PostgreSQL: %v", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	if err := db.PingContext(ctx); err != nil {
		cancel()
		log.Fatalf("failed to connect to PostgreSQL: %v", err)
	}
	cancel()

	log.Printf("connected to PostgreSQL database %s", pgDatabase)

	mux := http.NewServeMux()

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{
			"service": "jobs-api",
		})
	})

	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{
			"status": "ok",
		})
	})

	mux.HandleFunc("/ready", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), time.Second)
		defer cancel()

		if err := db.PingContext(ctx); err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{
				"status": "not ready",
			})
			return
		}

		writeJSON(w, http.StatusOK, map[string]string{
			"status": "ready",
		})
	})

	mux.HandleFunc("/jobs", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{
				"error": "method not allowed",
			})
			return
		}

		var req jobRequest

		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": "invalid request body",
			})
			return
		}

		if strings.TrimSpace(req.Payload) == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": "payload is required",
			})
			return
		}

		job := jobMessage{
			ID:        newJobID(),
			Payload:   req.Payload,
			CreatedAt: time.Now().UTC(),
		}

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		_, err := db.ExecContext(
			ctx,
			`INSERT INTO jobs (id, payload, status, created_at, updated_at)
			 VALUES ($1, $2, 'queued', $3, $3)`,
			job.ID,
			job.Payload,
			job.CreatedAt,
		)
		if err != nil {
			log.Printf("failed to persist job %s: %v", job.ID, err)

			writeJSON(w, http.StatusInternalServerError, map[string]string{
				"error": "failed to create job",
			})
			return
		}

		if err := publishJob(ctx, brokerURL, queue, job); err != nil {
			log.Printf("failed to publish job %s: %v", job.ID, err)

			_, updateErr := db.ExecContext(
				context.Background(),
				`UPDATE jobs
				 SET status = 'failed', updated_at = NOW()
				 WHERE id = $1`,
				job.ID,
			)
			if updateErr != nil {
				log.Printf("failed to mark job %s as failed: %v", job.ID, updateErr)
			}

			writeJSON(w, http.StatusServiceUnavailable, map[string]string{
				"error": "failed to queue job",
			})
			return
		}

		log.Printf("queued job %s", job.ID)

		writeJSON(w, http.StatusAccepted, map[string]string{
			"id":     job.ID,
			"status": "queued",
		})
	})

	mux.HandleFunc("/jobs/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{
				"error": "method not allowed",
			})
			return
		}

		id := strings.TrimPrefix(r.URL.Path, "/jobs/")
		if id == "" || strings.Contains(id, "/") {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": "invalid job id",
			})
			return
		}

		var job jobRecord

		err := db.QueryRowContext(
			r.Context(),
			`SELECT id, payload, status, created_at, updated_at
			 FROM jobs
			 WHERE id = $1`,
			id,
		).Scan(
			&job.ID,
			&job.Payload,
			&job.Status,
			&job.CreatedAt,
			&job.UpdatedAt,
		)

		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, map[string]string{
				"error": "job not found",
			})
			return
		}

		if err != nil {
			log.Printf("failed to read job %s: %v", id, err)

			writeJSON(w, http.StatusInternalServerError, map[string]string{
				"error": "failed to read job",
			})
			return
		}

		writeJSON(w, http.StatusOK, job)
	})

	log.Printf("jobs-api listening on :%s", port)

	if err := http.ListenAndServe(":"+port, mux); err != nil {
		log.Fatal(err)
	}
}

func getenv(key, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}

	return value
}
