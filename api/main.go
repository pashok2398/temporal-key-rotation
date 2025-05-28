package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"

	"temporal-worker/shared"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
)

func main() {
	http.HandleFunc("/submit", handler)
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	log.Printf("API server listening on :%s", port)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}

func handler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "only POST allowed", http.StatusMethodNotAllowed)
		return
	}

	var p shared.Payload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		http.Error(w, "Invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Validate payload
	if p.ID <= 0 || p.Name == "" || p.Email == "" {
		http.Error(w, "Invalid payload: ID, Name, and Email are required", http.StatusBadRequest)
		return
	}

	// Get configuration from environment variables
	codecServerURL := os.Getenv("CODEC_SERVER_URL")
	if codecServerURL == "" {
		codecServerURL = "http://localhost:8081"
	}

	temporalHostPort := os.Getenv("TEMPORAL_HOST_PORT")
	if temporalHostPort == "" {
		temporalHostPort = "localhost:7233"
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
		log.Printf("Temporal client error: %v", err)
		http.Error(w, "Temporal client error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer c.Close()

	workflowOptions := client.StartWorkflowOptions{
		ID:        fmt.Sprintf("payload-%d", p.ID),
		TaskQueue: "payload-task-queue",
	}

	we, err := c.ExecuteWorkflow(context.Background(), workflowOptions, "ProcessPayloadWorkflow", p)
	if err != nil {
		log.Printf("Workflow start error: %v", err)
		http.Error(w, "Workflow start error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	log.Printf("Started workflow %s for payload ID %d", we.GetID(), p.ID)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)

	response := map[string]string{
		"workflow_id": we.GetID(),
		"status":      "started",
	}
	json.NewEncoder(w).Encode(response)
}
