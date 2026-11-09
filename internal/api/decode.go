package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
)

// maxRequestBodyBytes caps request bodies. Real requests are well under 1 KiB;
// the limit stops a client from making the server buffer arbitrary input.
const maxRequestBodyBytes = 64 << 10

// mapRequest decodes the JSON request body into dto. It returns an error,
// and the caller should respond 400, when the body:
//   - is larger than maxRequestBodyBytes,
//   - is not valid JSON, or is valid JSON but not an object,
//   - holds anything other than whitespace after that object, such as a
//     second object,
//   - contains a field dto does not declare, or a field of the wrong type.
func mapRequest[T any](writer http.ResponseWriter, request *http.Request, dto *T) error {
	body := json.NewDecoder(http.MaxBytesReader(writer, request.Body, maxRequestBodyBytes))

	// Read the first value as raw bytes so it can be checked before it is
	// mapped onto dto; decoding null into a struct would silently succeed.
	var raw json.RawMessage
	if err := body.Decode(&raw); err != nil {
		return fmt.Errorf("read request body: %w", err)
	}

	if raw[0] != '{' {
		return errors.New("request body must be a JSON object")
	}

	// Decoding again must reach the end of the body; any other outcome means
	// something follows the object.
	if err := body.Decode(&json.RawMessage{}); !errors.Is(err, io.EOF) {
		return errors.New("request body must contain a single JSON object")
	}

	fields := json.NewDecoder(bytes.NewReader(raw))
	fields.DisallowUnknownFields()

	if err := fields.Decode(dto); err != nil {
		return fmt.Errorf("map request body: %w", err)
	}

	return nil
}
