package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	amqp "github.com/Azure/go-amqp"
	_ "github.com/jackc/pgx/v5/stdlib"
)

type jobMessage struct {
	ID        string    `json:"id"`
	Payload   string    `json:"payload"`
	CreatedAt time.Time `json:"created_at"`
}

type jobResult struct {
	ID          string    `json:"id"`
	Payload     string    `json:"payload"`
	Result      string    `json:"result"`
	Status      string    `json:"status"`
	ProcessedAt time.Time `json:"processed_at"`
}

func main() {
	brokerURL := getenv(
		"SERVICEBUS_URL",
		"amqp://servicebus:5672",
	)
	queue := getenv("SERVICEBUS_QUEUE", "jobs")

	blobURL := strings.TrimRight(
		getenv(
			"BLOB_URL",
			"http://blob-storage:4577/devstoreaccount1",
		),
		"/",
	)
	blobContainer := getenv(
		"BLOB_CONTAINER",
		"job-results",
	)

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

	ctx, cancel := context.WithTimeout(
		context.Background(),
		5*time.Second,
	)

	if err := db.PingContext(ctx); err != nil {
		cancel()
		log.Fatalf("failed to connect to PostgreSQL: %v", err)
	}

	cancel()

	log.Printf(
		"connected to PostgreSQL database %s",
		pgDatabase,
	)

	ctx = context.Background()

	for {
		err := consume(
			ctx,
			db,
			brokerURL,
			queue,
			blobURL,
			blobContainer,
		)

		if err != nil {
			log.Printf("worker error: %v", err)
			log.Printf("retrying in 5 seconds...")
			time.Sleep(5 * time.Second)
		}
	}
}

func consume(
	ctx context.Context,
	db *sql.DB,
	brokerURL string,
	queue string,
	blobURL string,
	blobContainer string,
) error {
	log.Printf("connecting to %s", brokerURL)

	conn, err := amqp.Dial(
		ctx,
		brokerURL,
		&amqp.ConnOptions{
			SASLType: amqp.SASLTypeAnonymous(),
		},
	)
	if err != nil {
		return err
	}
	defer conn.Close()

	session, err := conn.NewSession(ctx, nil)
	if err != nil {
		return err
	}
	defer session.Close(ctx)

	receiver, err := session.NewReceiver(
		ctx,
		"/"+queue,
		nil,
	)
	if err != nil {
		return err
	}
	defer receiver.Close(ctx)

	log.Printf(
		"worker ready; waiting for messages on queue %q",
		queue,
	)

	for {
		msg, err := receiver.Receive(ctx, nil)
		if err != nil {
			return err
		}

		var job jobMessage

		if err := json.Unmarshal(
			msg.GetData(),
			&job,
		); err != nil {
			return fmt.Errorf(
				"decode job message: %w",
				err,
			)
		}

		log.Printf("received job %s", job.ID)

		if err := updateJobStatus(
			db,
			job.ID,
			"processing",
		); err != nil {
			return fmt.Errorf(
				"mark job %s processing: %w",
				job.ID,
				err,
			)
		}

		log.Printf("job %s processing", job.ID)

		time.Sleep(time.Second)

		result := jobResult{
			ID:      job.ID,
			Payload: job.Payload,
			Result: fmt.Sprintf(
				"processed: %s",
				job.Payload,
			),
			Status:      "completed",
			ProcessedAt: time.Now().UTC(),
		}

		blobName := job.ID + ".json"

		if err := uploadResult(
			blobURL,
			blobContainer,
			blobName,
			result,
		); err != nil {
			_ = updateJobStatus(
				db,
				job.ID,
				"failed",
			)

			return fmt.Errorf(
				"upload result for job %s: %w",
				job.ID,
				err,
			)
		}

		blobRef := blobContainer + "/" + blobName

		if err := completeJob(
			db,
			job.ID,
			blobRef,
		); err != nil {
			return fmt.Errorf(
				"complete job %s: %w",
				job.ID,
				err,
			)
		}

		if err := receiver.AcceptMessage(
			ctx,
			msg,
		); err != nil {
			return fmt.Errorf(
				"acknowledge job %s: %w",
				job.ID,
				err,
			)
		}

		log.Printf(
			"job %s completed; result=%s",
			job.ID,
			blobRef,
		)
	}
}

func uploadResult(
	baseURL string,
	container string,
	blobName string,
	result jobResult,
) error {
	body, err := json.Marshal(result)
	if err != nil {
		return err
	}

	url := fmt.Sprintf(
		"%s/%s/%s",
		baseURL,
		container,
		blobName,
	)

	req, err := http.NewRequest(
		http.MethodPut,
		url,
		bytes.NewReader(body),
	)
	if err != nil {
		return err
	}

	req.Header.Set(
		"x-ms-version",
		"2023-11-03",
	)
	req.Header.Set(
		"x-ms-date",
		time.Now().UTC().Format(http.TimeFormat),
	)
	req.Header.Set(
		"x-ms-blob-type",
		"BlockBlob",
	)
	req.Header.Set(
		"Authorization",
		"SharedKey devstoreaccount1:development",
	)
	req.Header.Set(
		"Content-Type",
		"application/json",
	)

	client := &http.Client{
		Timeout: 10 * time.Second,
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 ||
		resp.StatusCode >= 300 {
		return fmt.Errorf(
			"blob storage returned HTTP %d",
			resp.StatusCode,
		)
	}

	return nil
}

func updateJobStatus(
	db *sql.DB,
	id string,
	status string,
) error {
	ctx, cancel := context.WithTimeout(
		context.Background(),
		5*time.Second,
	)
	defer cancel()

	result, err := db.ExecContext(
		ctx,
		`UPDATE jobs
		 SET status = $1,
		     updated_at = NOW()
		 WHERE id = $2`,
		status,
		id,
	)
	if err != nil {
		return err
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}

	if rows != 1 {
		return fmt.Errorf(
			"expected to update 1 row, updated %d",
			rows,
		)
	}

	return nil
}

func completeJob(
	db *sql.DB,
	id string,
	blobRef string,
) error {
	ctx, cancel := context.WithTimeout(
		context.Background(),
		5*time.Second,
	)
	defer cancel()

	result, err := db.ExecContext(
		ctx,
		`UPDATE jobs
		 SET status = 'completed',
		     result_blob = $1,
		     updated_at = NOW()
		 WHERE id = $2`,
		blobRef,
		id,
	)
	if err != nil {
		return err
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}

	if rows != 1 {
		return fmt.Errorf(
			"expected to update 1 row, updated %d",
			rows,
		)
	}

	return nil
}

func getenv(
	key string,
	fallback string,
) string {
	value := os.Getenv(key)

	if value == "" {
		return fallback
	}

	return value
}
