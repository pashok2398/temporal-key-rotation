package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"

	"temporal-key-rotation/shared"

	commonpb "go.temporal.io/api/common/v1"
)

// RemoteCodecClient implements the PayloadCodec interface
type RemoteCodecClient struct {
	endpoint   string
	httpClient *http.Client
}

// NewRemoteCodecClient creates a new remote codec client
func NewRemoteCodecClient(endpoint string) *RemoteCodecClient {
	return &RemoteCodecClient{
		endpoint:   endpoint,
		httpClient: &http.Client{},
	}
}

// Encode implements the PayloadCodec interface
func (c *RemoteCodecClient) Encode(payloads []*commonpb.Payload) ([]*commonpb.Payload, error) {
	if len(payloads) == 0 {
		return payloads, nil
	}

	// Convert Temporal payloads to codec request format
	request := shared.CodecRequest{
		Payloads: make([]shared.PayloadData, len(payloads)),
	}

	for i, payload := range payloads {
		metadata := make(map[string]string)
		for key, value := range payload.Metadata {
			metadata[key] = string(value)
		}

		// Encode binary data as base64 for safe transport
		request.Payloads[i] = shared.PayloadData{
			Metadata: metadata,
			Data:     base64.StdEncoding.EncodeToString(payload.Data),
		}
	}

	// Send encode request to codec server
	response, err := c.sendRequest("/encode", request)
	if err != nil {
		return nil, fmt.Errorf("encode request failed: %w", err)
	}

	// Convert response back to Temporal payloads
	result := make([]*commonpb.Payload, len(response.Payloads))
	for i, payloadData := range response.Payloads {
		metadata := make(map[string][]byte)
		for key, value := range payloadData.Metadata {
			metadata[key] = []byte(value)
		}

		// Decode base64 data back to binary
		data, err := base64.StdEncoding.DecodeString(payloadData.Data)
		if err != nil {
			return nil, fmt.Errorf("failed to decode base64 data: %w", err)
		}

		result[i] = &commonpb.Payload{
			Metadata: metadata,
			Data:     data,
		}
	}

	return result, nil
}

// Decode implements the PayloadCodec interface
func (c *RemoteCodecClient) Decode(payloads []*commonpb.Payload) ([]*commonpb.Payload, error) {
	if len(payloads) == 0 {
		return payloads, nil
	}

	// Convert Temporal payloads to codec request format
	request := shared.CodecRequest{
		Payloads: make([]shared.PayloadData, len(payloads)),
	}

	for i, payload := range payloads {
		metadata := make(map[string]string)
		for key, value := range payload.Metadata {
			metadata[key] = string(value)
		}

		// Encode binary data as base64 for safe transport
		request.Payloads[i] = shared.PayloadData{
			Metadata: metadata,
			Data:     base64.StdEncoding.EncodeToString(payload.Data),
		}
	}

	// Send decode request to codec server
	response, err := c.sendRequest("/decode", request)
	if err != nil {
		return nil, fmt.Errorf("decode request failed: %w", err)
	}

	// Convert response back to Temporal payloads
	result := make([]*commonpb.Payload, len(response.Payloads))
	for i, payloadData := range response.Payloads {
		metadata := make(map[string][]byte)
		for key, value := range payloadData.Metadata {
			metadata[key] = []byte(value)
		}

		// Decode base64 data back to binary
		data, err := base64.StdEncoding.DecodeString(payloadData.Data)
		if err != nil {
			return nil, fmt.Errorf("failed to decode base64 data: %w", err)
		}

		result[i] = &commonpb.Payload{
			Metadata: metadata,
			Data:     data,
		}
	}

	return result, nil
}

// sendRequest sends a request to the codec server
func (c *RemoteCodecClient) sendRequest(endpoint string, request shared.CodecRequest) (*shared.CodecResponse, error) {
	reqBody, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	resp, err := c.httpClient.Post(
		c.endpoint+endpoint,
		"application/json",
		bytes.NewBuffer(reqBody),
	)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("codec server returned status %d", resp.StatusCode)
	}

	var response shared.CodecResponse
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &response, nil
}
