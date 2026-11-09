package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// maxRequestBodyBytes caps request bodies. Real requests are well under 1 KiB;
// the limit stops a client from making the server buffer arbitrary input.
const maxRequestBodyBytes = 64 << 10

// errTrailingData reports a body that holds more than one JSON value.
var errTrailingData = errors.New("trailing data after JSON value")

// decodeRequest decodes the JSON request body into dto. The body must be a
// single JSON value of at most maxRequestBodyBytes, with no fields that dto
// does not declare. If decoding fails it writes the error response itself
// and returns false: 413 when the body is too large, 400 otherwise.
func decodeRequest[T any](writer http.ResponseWriter, request *http.Request, dto *T) bool {
	err := decodeStrict(http.MaxBytesReader(writer, request.Body, maxRequestBodyBytes), dto)
	if err == nil {
		return true
	}

	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		http.Error(writer, "Request body too large", http.StatusRequestEntityTooLarge)
		return false
	}

	http.Error(writer, describeDecodeError(err), http.StatusBadRequest)

	return false
}

// decodeStrict decodes exactly one JSON value from body into dto and fails
// on unknown fields or on anything but whitespace after that value.
func decodeStrict(body io.Reader, dto any) error {
	decoder := json.NewDecoder(body)
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(dto); err != nil {
		return err
	}

	// A second Decode must hit the end of the body. Anything else, whether
	// another value like {"id":"a"}{"id":"b"} or stray text, is trailing data,
	// unless reading it ran past the size limit.
	var extra json.RawMessage

	err := decoder.Decode(&extra)
	if errors.Is(err, io.EOF) {
		return nil
	}

	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		return err
	}

	return errTrailingData
}

// describeDecodeError turns a decoding error into a message that tells the
// client what to fix without exposing Go type names.
func describeDecodeError(err error) string {
	var syntaxErr *json.SyntaxError
	var typeErr *json.UnmarshalTypeError

	switch {
	case errors.Is(err, io.EOF):
		return "Request body is empty"
	case errors.Is(err, io.ErrUnexpectedEOF), errors.As(err, &syntaxErr):
		return "Request body is not valid JSON"
	case errors.As(err, &typeErr):
		if typeErr.Field == "" {
			return "Request body must be a JSON object"
		}

		return fmt.Sprintf("Field %q has the wrong type", typeErr.Field)
	case errors.Is(err, errTrailingData):
		return "Request body must contain a single JSON object"
	}

	// encoding/json has no error type for unknown fields, only this message
	// format, so matching the text is the only way to recognize it.
	if field, ok := strings.CutPrefix(err.Error(), "json: unknown field "); ok {
		return "Unknown field " + field
	}

	return "Request body is not valid JSON"
}
