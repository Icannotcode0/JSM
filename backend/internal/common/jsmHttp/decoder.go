package jsmHttp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

// MaxBodyBytes caps how much of a request body will be read into memory.
//
// 1 MiB is far more than any JSON body this API accepts. The cap exists because
// io.ReadAll on a raw request body is unbounded: a single request advertising a
// multi-gigabyte body would otherwise be buffered in full before json.Unmarshal
// ever ran.
const MaxBodyBytes = 1 << 20

// ErrBodyTooLarge is returned by Decode when the body exceeds MaxBodyBytes.
// Handlers should map it to 413.
var ErrBodyTooLarge = errors.New("request body too large")

// LimitBody caps r.Body in place and should be called before Decode on any
// route that reads one.
//
// This is the stronger of the two guards: MaxBytesReader stops the *connection*
// once the limit is passed, so an oversized upload is cut off mid-flight rather
// than being received and then discarded. Decode's own check still stands on
// its own for callers that never reach here.
func LimitBody(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, MaxBodyBytes)
}

func Decode(ctx context.Context, req io.Reader, dest any) error {
	// One byte past the limit, so "exactly at the cap" and "over it" are
	// distinguishable, LimitReader alone would silently truncate an oversized
	// body into a confusing JSON syntax error.
	contents, err := io.ReadAll(io.LimitReader(req, MaxBodyBytes+1))
	if err != nil {
		return err
	}
	if len(contents) > MaxBodyBytes {
		return ErrBodyTooLarge
	}
	return json.Unmarshal(contents, dest)
}
