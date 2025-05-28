package main

import (
	"database/sql"
	"fmt"
	"log"

	"temporal-worker/shared"
)

type Activities struct {
	DB *sql.DB
}

func (a *Activities) InsertPayload(p shared.Payload) error {
	log.Printf("Inserting payload: ID=%d, Name=%s, Email=%s", p.ID, p.Name, p.Email)

	// Use UPSERT to handle potential duplicate IDs
	query := `
		INSERT INTO payloads (id, name, email) 
		VALUES ($1, $2, $3) 
		ON CONFLICT (id) 
		DO UPDATE SET name = EXCLUDED.name, email = EXCLUDED.email
	`

	_, err := a.DB.Exec(query, p.ID, p.Name, p.Email)
	if err != nil {
		return fmt.Errorf("insert failed: %w", err)
	}

	log.Printf("Successfully inserted/updated payload with ID=%d", p.ID)
	return nil
}
