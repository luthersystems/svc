// Copyright © 2021 Luther Systems, Ltd. All right reserved.

package mailer

import (
	"context"
	"os"
	"testing"
	"time"
)

const (
	reqTimeout          = 30 * time.Second
	DefaultSuccessEmail = "success@simulator.amazonses.com"
	SESRegion           = "eu-west-1"
	EmailSender         = "noreply@testing.luthersystemsapp.com"
	SubjectTemplateText = `Test Email GoTest`
	TextTemplateText    = `Dear SampleUsername,

This is a test from GoTest.

Sincerely,
SampleSenderName
`
	HTMLTemplateText = `<p>Dear SampleUserName,</p>
<p>
This is a <b>test</b> from GoTest.
</p>
<p>
Sincerely, <br>
<i>SampleSenderName</i>
</p>
`
)

// TestSend makes a call to AWS SES to send an email.
// IMPORTANT: The env variable `MAILER_SES_TESTS` must be set in order
// to activate this test. This guard is to prevent the automated tests
// failing in CI, or spamming when running tests.
// NOTE: The env variable `MAILER_SES_RECIPIENT` can also be set to
// send to a specific email address
func TestSend(t *testing.T) {
	if os.Getenv("MAILER_SES_TESTS") == "" {
		t.Skip("Skipping test: $MAILER_SES_TESTS not set")
	}
	recipient := DefaultSuccessEmail
	if os.Getenv("MAILER_SES_RECIPIENT") != "" {
		recipient = os.Getenv("MAILER_SES_RECIPIENT")
	}
	mailer, err := NewSES(SESRegion, EmailSender)
	if err != nil {
		t.Fatalf("init mailer: %v", err)
	}
	ctx, done := context.WithTimeout(context.Background(), reqTimeout)
	defer done()
	err = mailer.Send(ctx, HTMLTemplateText, recipient, SubjectTemplateText)
	if err != nil {
		t.Fatalf("send mailer: %v", err)
	}
	t.Logf("Sent email to: %s", recipient)
}
