package gemini

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"google.golang.org/genai"
	"google.golang.org/genai/interactions/models/apierrors"

	"github.com/sonnes/gopi/internal/httpapi"
)

// Error is a failed answer of the API. Each method of the provider
// returns a failed answer as an *Error. Read it with errors.As. It
// unwraps to the error of the SDK. An error event in a stream that
// started is not an *Error, because its answer did not fail.
type Error struct {
	// StatusCode is the HTTP status of the answer.
	StatusCode int
	// Message is the message of the error body. If the body has no
	// message, it is the body.
	Message string
	// Header is the header of the answer. It is nil for an image call
	// and for a model list call, because the models API of the SDK does
	// not give the header.
	Header http.Header
	err    error
}

// Error returns the status and the message of the answer.
func (e *Error) Error() string {
	return fmt.Sprintf("status %d: %s", e.StatusCode, e.Message)
}

// Unwrap returns the error of the SDK.
func (e *Error) Unwrap() error {
	return e.err
}

// apiError returns an [*Error] for an API error of the SDK. It returns
// each other error unchanged, and nil for nil.
//
// The Interactions client returns a typed error for a 4xx or a 5xx
// answer with an error body. It returns [*apierrors.APIError] for each
// other failed answer. If the SDK cannot read the error body, it returns
// [*apierrors.ResponseValidationError]. The API sends some error bodies
// as a JSON array, such as a 402 for a project with no credits. It sends
// others as an error event, such as a 400 for a stream request. The
// models API returns [genai.APIError].
func apiError(err error) error {
	var (
		clientErr     *apierrors.CreateInteractionClientError
		serverErr     *apierrors.CreateInteractionServerError
		apiErr        *apierrors.APIError
		validationErr *apierrors.ResponseValidationError
		modelErr      genai.APIError
	)
	switch {
	case errors.As(err, &clientErr):
		return responseError(
			clientErr.HTTPMeta.Response,
			clientErr.Error_.Message,
			err,
		)
	case errors.As(err, &serverErr):
		return responseError(
			serverErr.HTTPMeta.Response,
			serverErr.Error_.Message,
			err,
		)
	case errors.As(err, &apiErr):
		return &Error{
			StatusCode: apiErr.StatusCode,
			Message:    bodyMessage(apiErr.Body),
			Header:     responseHeader(apiErr.RawResponse),
			err:        err,
		}
	case errors.As(err, &validationErr):
		return &Error{
			StatusCode: validationErr.StatusCode,
			Message:    bodyMessage(validationErr.Body),
			Header:     responseHeader(validationErr.RawResponse),
			err:        err,
		}
	case errors.As(err, &modelErr):
		return &Error{
			StatusCode: modelErr.Code,
			Message:    bodyMessage(modelErr.Message),
			err:        err,
		}
	default:
		return err
	}
}

// statusError returns an [*Error] for a failed answer, and each other
// error unchanged. If the body has no message, the message is the text
// of the status.
func statusError(err error) error {
	var status *httpapi.StatusError
	if !errors.As(err, &status) {
		return err
	}
	message := strings.TrimSpace(bodyMessage(status.Body))
	if message == "" {
		message = http.StatusText(status.Code)
	}
	return &Error{
		StatusCode: status.Code,
		Message:    message,
		Header:     status.Header,
		err:        err,
	}
}

// If the error has no message, the message is the text of the status.
func responseError(resp *http.Response, message *string, err error) *Error {
	var status int
	if resp != nil {
		status = resp.StatusCode
	}

	text := http.StatusText(status)
	if message != nil && strings.TrimSpace(*message) != "" {
		text = strings.TrimSpace(*message)
	}

	return &Error{
		StatusCode: status,
		Message:    text,
		Header:     responseHeader(resp),
		err:        err,
	}
}

func responseHeader(resp *http.Response) http.Header {
	if resp == nil {
		return nil
	}
	return resp.Header
}

type errorBody struct {
	Error struct {
		Message string `json:"message"`
	} `json:"error"`
}

const sseData = "data:"

// bodyMessage returns the message of an error body: an object, the
// first item of an array, or the data of an error event. If the body has
// no message, it returns the body.
func bodyMessage(body string) string {
	for line := range strings.Lines(body) {
		if data, ok := strings.CutPrefix(line, sseData); ok {
			body = strings.TrimSpace(data)
			break
		}
	}

	var one errorBody
	if err := json.Unmarshal([]byte(body), &one); err == nil && one.Error.Message != "" {
		return strings.TrimSpace(one.Error.Message)
	}
	var list []errorBody
	hasMessage := json.Unmarshal([]byte(body), &list) == nil &&
		len(list) > 0 &&
		list[0].Error.Message != ""
	if hasMessage {
		return strings.TrimSpace(list[0].Error.Message)
	}
	return body
}
