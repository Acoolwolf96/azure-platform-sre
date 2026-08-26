package main

import (
	"context"
	"log"
	"os"
	"time"

	amqp "github.com/Azure/go-amqp"
)

func main() {
	brokerURL := os.Getenv("SERVICEBUS_URL")
	if brokerURL == "" {
		brokerURL = "amqp://servicebus:5672"
	}

	queue := os.Getenv("SERVICEBUS_QUEUE")
	if queue == "" {
		queue = "jobs"
	}

	ctx := context.Background()

	for {
		if err := consume(ctx, brokerURL, queue); err != nil {
			log.Printf("worker connection error: %v", err)
			log.Printf("retrying in 5 seconds...")
			time.Sleep(5 * time.Second)
		}
	}
}

func consume(ctx context.Context, brokerURL, queue string) error {
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

		log.Printf("received job: %s", string(msg.GetData()))

		if err := receiver.AcceptMessage(ctx, msg); err != nil {
			return err
		}
	}
}
