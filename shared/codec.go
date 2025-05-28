package shared

// CodecRequest represents the request structure for codec operations
type CodecRequest struct {
	Payloads []PayloadData `json:"payloads"`
}

// CodecResponse represents the response structure for codec operations
type CodecResponse struct {
	Payloads []PayloadData `json:"payloads"`
}

// PayloadData represents individual payload data
type PayloadData struct {
	Metadata         map[string]string `json:"metadata"`
	Data             string            `json:"data"` // base64 encoded
	KMSKeyID         string            `json:"kms_key_id,omitempty"`
	EncryptedDataKey string            `json:"encrypted_data_key,omitempty"`
	Algorithm        string            `json:"algorithm,omitempty"`
}
