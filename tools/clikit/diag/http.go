package diag

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
)

// WriteHTTPJSON renders and encodes the response before committing its status or headers.
// A failed response encode is replaced with a caller-wrapped typed 500 diagnostic.
func WriteHTTPJSON(w http.ResponseWriter, status int, body any, encodingFailure func(error) any) error {
	encoded, err := encodeHTTPJSON(body)
	if err != nil {
		status = http.StatusInternalServerError
		encoded, err = encodeHTTPJSON(encodingFailure(err))
		if err != nil {
			return fmt.Errorf("encode fallback diagnostic response: %w", err)
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, err = w.Write(encoded)
	return err
}

func encodeHTTPJSON(value any) ([]byte, error) {
	var encoded bytes.Buffer
	if err := json.NewEncoder(&encoded).Encode(value); err != nil {
		return nil, err
	}
	return encoded.Bytes(), nil
}
