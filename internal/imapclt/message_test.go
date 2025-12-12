package imapclt

import (
	"errors"
	"fmt"
	"io"
	"os"
	"testing"
	"time"

	"github.com/fho/rspamd-iscan/internal/testutils/assert"
	"github.com/fho/rspamd-iscan/internal/testutils/mail"
)

func testMailData(t *testing.T) []byte {
	data, err := os.ReadFile(mail.TestHamMailPath(t))
	assert.NoError(t, err)
	return data
}

func TestMessages(t *testing.T) {
	testMailPath := mail.TestHamMailPath(t)
	srv, clt := startServerClient(t)

	assert.NoError(t, clt.Upload(testMailPath, srv.InboxMailBox, time.Now()))
	assert.NoError(t, clt.Upload(testMailPath, srv.InboxMailBox, time.Now()))
	assert.NoError(t, clt.Upload(testMailPath, srv.InboxMailBox, time.Now()))

	cnt := 0
	for msg, err := range clt.Messages(srv.InboxMailBox) {
		assert.NoError(t, err)
		if msg.UID == 0 {
			t.Error("msg.uid is 0")
		}
		body, err := io.ReadAll(msg.Message)
		assert.NoError(t, err)

		assert.NotEqual(t, msg.UID, 0)
		assert.NotEqual(t, len(body), 0)
		expectedMail := testMailData(t)
		assert.Equal(t, string(expectedMail), string(body))
		assert.Equal(t, testMailSubject, msg.Envelope.Subject)
		assert.Equal(t, 1, len(msg.Envelope.Recipients))
		assert.Equal(t, testMailRecipient, msg.Envelope.Recipients[0])
		assert.Equal(t, 1, len(msg.Envelope.From))
		assert.Equal(t, testMailSender, msg.Envelope.From[0])
		cnt++
	}
	assert.Equal(t, 3, cnt)
}

func TestIsMalformedEnvelopeErr(t *testing.T) {
	t.Run("wrapped sentinel", func(t *testing.T) {
		err := fmt.Errorf("x: %w", errMalformedEnvelope)
		if !isMalformedEnvelopeErr(err) {
			t.Fatalf("expected malformed envelope error to be detected")
		}
		if !errors.Is(err, errMalformedEnvelope) {
			t.Fatalf("expected errors.Is to match sentinel")
		}
	})

	t.Run("imapwire expected paren", func(t *testing.T) {
		err := errors.New(`in response-data: in envelope: imapwire: expected ')', got "7"`)
		if !isMalformedEnvelopeErr(err) {
			t.Fatalf("expected imapwire envelope parse error to be detected")
		}
	})
}
