package shared

// Payload represents the data structure used across the application
type Payload struct {
	ID    int    `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}
