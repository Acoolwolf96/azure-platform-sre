package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"time"

	amqp "github.com/Azure/go-amqp"
	_ "github.com/jackc/pgx/v5/stdlib"
)

type jobMessage struct {
	ID        string    `json:"id"`
	Payload   string    `json:"payload"`
	CreatedAt time.Time `json:"created_at"`
}

func main() {
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

	ctx = context.Background()

	for {
		if err := consume(ctx, db, brokerURL, queue); err != nil {
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
) error {
	log.Printf("connecting to %s", brokerURL)

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

	receiver, err := session.NewReceiver(ctx, "/"+queue, nil)
	if err != nil {
		return err
	}
	defer receiver.Close(ctx)

	log.Printf("worker ready; waiting for messages on queue %q", queue)

	for {
		msg, err := receiver.Receive(ctx, nil)
		if err != nil {
			return err
		}

		var job jobMessage

		if err := json.Unmarshal(msg.GetData(), &job); err != nil {
			return fmt.Errorf("decode job message: %w", err)
		}

		log.Printf("received job %s", job.ID)

		if err := updateJobStatus(db, job.ID, "processing"); err != nil {
			return fmt.Errorf("mark job %s processing: %w", job.ID, err)
		}

		log.Printf("job %s processing", job.ID)

		// Simulated application work.
		time.Sleep(1 * time.Second)

		if err := updateJobStatus(db, job.ID, "completed"); err != nil {
			return fmt.Errorf("mark job %s completed: %w", job.ID, err)
		}

		if err := receiver.AcceptMessage(ctx, msg); err != nil {
			return fmt.Errorf("acknowledge job %s: %w", job.ID, err)
		}

		log.Printf("job %s completed", job.ID)
	}
}

func updateJobStatus(db *sql.DB, id, status string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := db.ExecContext(
		ctx,
		`UPDATE jobs
		 SET status = $1, updated_at = NOW()
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
		return fmt.Errorf("expected to update 1 row, updated %d", rows)
	}

	return nil
}

func getenv(key, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}

	return value
}
