package main

import (
	"database/sql"
	"log"
	"os"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/worker"

	_ "github.com/lib/pq"
)

func main() {
	// Get configuration from environment variables
	codecServerURL := os.Getenv("CODEC_SERVER_URL")
	if codecServerURL == "" {
		codecServerURL = "http://localhost:8081"
	}

	temporalHostPort := os.Getenv("TEMPORAL_HOST_PORT")
	if temporalHostPort == "" {
		temporalHostPort = "localhost:7233"
	}

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://postgres:temporalpass@my-postgres-postgresql.default.svc.cluster.local:5432/postgres?sslmode=disable"
	}

	// Create a data converter with codec support
	codecClient := NewRemoteCodecClient(codecServerURL)
	codecConverter := converter.NewCodecDataConverter(
		converter.GetDefaultDataConverter(),
		codecClient,
	)

	// Connect to Temporal with codec support
	c, err := client.Dial(client.Options{
		HostPort:      temporalHostPort,
		Namespace:     "default",
		DataConverter: codecConverter,
	})
	if err != nil {
		log.Fatalf("unable to create Temporal client: %v", err)
	}
	defer c.Close()

	// Connect to Postgres
	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		log.Fatalf("unable to connect to DB: %v", err)
	}
	defer db.Close()

	// Test database connection
	if err := db.Ping(); err != nil {
		log.Fatalf("unable to ping database: %v", err)
	}

	activities := &Activities{DB: db}

	// Create worker (codec support comes from the client)
	w := worker.New(c, "payload-task-queue", worker.Options{})
	w.RegisterWorkflow(ProcessPayloadWorkflow)
	w.RegisterActivity(activities.InsertPayload)

	log.Printf("Worker started with codec support (codec server: %s)...", codecServerURL)
	if err := w.Run(worker.InterruptCh()); err != nil {
		log.Fatalf("worker failed: %v", err)
	}
}
