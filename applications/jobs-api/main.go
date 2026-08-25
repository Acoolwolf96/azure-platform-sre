package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"time"

	amqp "github.com/Azure/go-amqp"
)

type jobRequest struct {
	Payload string `json:"payload"`
}

type jobMessage struct {
	ID        string    `json:"id"`
	Payload   string    `json:"payload"`
	CreatedAt time.Time `json:"created_at"`
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
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	brokerURL := os.Getenv("SERVICEBUS_URL")
	if brokerURL == "" {
		brokerURL = "amqp://servicebus:5672"
	}

	queue := os.Getenv("SERVICEBUS_QUEUE")
	if queue == "" {
		queue = "jobs"
	}

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

		job := jobMessage{
			ID:        newJobID(),
			Payload:   req.Payload,
			CreatedAt: time.Now().UTC(),
		}

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		if err := publishJob(ctx, brokerURL, queue, job); err != nil {
			log.Printf("failed to publish job: %v", err)

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

	log.Printf("jobs-api listening on :%s", port)

	if err := http.ListenAndServe(":"+port, mux); err != nil {
		log.Fatal(err)
	}
}
