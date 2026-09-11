package mailer

import (
	"context"
	"errors"
	"testing"
)

// Every implementation must satisfy the interface. This fails at compile time
// rather than when something is wired up, which is the point of having the
// seam before there is a real transport.
var (
	_ Mailer = (*LogMailer)(nil)
	_ Mailer = (*DiscardMailer)(nil)
	_ Mailer = (*RecordingMailer)(nil)
)

func TestRecordingMailerCapturesMessages(t *testing.T) {
	m := NewRecordingMailer()

	if _, ok := m.Last(); ok {
		t.Error("Last reported a message before anything was sent")
	}

	for _, to := range []string{"first@example.com", "second@example.com"} {
		if err := m.Send(context.Background(), Message{To: to, Subject: "Verify", Body: "123456"}); err != nil {
			t.Fatal(err)
		}
	}

	if len(m.Sent) != 2 {
		t.Fatalf("recorded %d messages, want 2", len(m.Sent))
	}
	last, ok := m.Last()
	if !ok || last.To != "second@example.com" {
		t.Errorf("Last = %+v, want the second message", last)
	}
	if last.Body != "123456" {
		t.Errorf("body = %q, want the code to survive intact", last.Body)
	}
}

// A caller that ignores the error would report a successful signup for mail
// that never left, so failure has to be reachable in tests.
func TestRecordingMailerCanFail(t *testing.T) {
	boom := errors.New("transport refused")
	m := &RecordingMailer{Err: boom}

	if err := m.Send(context.Background(), Message{To: "a@example.com"}); !errors.Is(err, boom) {
		t.Fatalf("got %v, want the configured error", err)
	}
	if len(m.Sent) != 0 {
		t.Error("a failed send was still recorded")
	}
}

func TestDiscardMailerSucceedsSilently(t *testing.T) {
	if err := NewDiscardMailer().Send(context.Background(), Message{To: "a@example.com"}); err != nil {
		t.Fatalf("DiscardMailer should never fail: %v", err)
	}
}

func TestLogMailerSucceeds(t *testing.T) {
	// Writes to the standard logger; this asserts only that it reports success,
	// since "did it print" is not behaviour worth pinning.
	if err := NewLogMailer().Send(context.Background(), Message{
		To: "a@example.com", Subject: "Verify your email", Body: "code: 123456",
	}); err != nil {
		t.Fatalf("LogMailer should never fail: %v", err)
	}
}
