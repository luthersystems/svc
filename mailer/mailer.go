// Copyright © 2025 Luther Systems, Ltd. All right reserved.

package mailer

import (
	"bytes"
	"context"
	"fmt"
	"mime/multipart"
	"net/textproto"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ses"
	"github.com/aws/aws-sdk-go-v2/service/ses/types"
)

const (
	// CharSet is the character set used in all SES emails.
	CharSet = "UTF-8"
)

// SES sends email notifications via AWS SES.
type SES struct {
	sender string
	svc    *ses.Client
}

// NewSES constructs a new mailer that uses AWS SES to send emails.
func NewSES(region string, sender string) (*SES, error) {
	cfg, err := config.LoadDefaultConfig(context.Background(),
		config.WithRegion(region))
	if err != nil {
		return nil, err
	}
	return &SES{
		sender: sender,
		svc:    ses.NewFromConfig(cfg),
	}, nil
}

// Send send an email to a person.
func (m *SES) Send(ctx context.Context, content string, email string, subject string) error {
	// Assemble the email.
	input := &ses.SendEmailInput{
		Destination: &types.Destination{
			CcAddresses: []string{},
			ToAddresses: []string{email},
		},
		Message: &types.Message{
			Body: &types.Body{
				Html: &types.Content{
					Charset: aws.String(CharSet),
					Data:    aws.String(content),
				},
			},
			Subject: &types.Content{
				Charset: aws.String(CharSet),
				Data:    aws.String(subject),
			},
		},
		Source: aws.String(m.sender),
	}
	// Attempt to send the email.
	_, err := m.svc.SendEmail(ctx, input)
	if err != nil {
		return err
	}
	return nil
}

// Attachment represents a file to attach to the email.
type Attachment struct {
	Filename string
	Data     []byte
}

// SendWithAttachment sends an email with one or more attachments.
func (m *SES) SendWithAttachment(ctx context.Context, body, to, subject string, attachments []Attachment) error {
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	// Set email headers
	mimeHeaders := make(map[string]string)
	mimeHeaders["From"] = m.sender
	mimeHeaders["To"] = to
	mimeHeaders["Subject"] = subject
	mimeHeaders["MIME-Version"] = "1.0"
	mimeHeaders["Content-Type"] = "multipart/mixed; boundary=" + writer.Boundary()

	// Write email headers
	var msg bytes.Buffer
	for k, v := range mimeHeaders {
		fmt.Fprintf(&msg, "%s: %s\r\n", k, v)
	}
	msg.WriteString("\r\n")

	// Write HTML body part
	bodyWriter, _ := writer.CreatePart(textproto.MIMEHeader{
		"Content-Type": {"text/html; charset=utf-8"},
	})
	if _, err := bodyWriter.Write([]byte(body)); err != nil {
		return fmt.Errorf("write: %w", err)
	}

	// Attach files
	for _, att := range attachments {
		partHeader := textproto.MIMEHeader{}
		partHeader.Set("Content-Type", "application/zip")
		partHeader.Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, att.Filename))
		part, _ := writer.CreatePart(partHeader)
		if _, err := part.Write(att.Data); err != nil {
			return fmt.Errorf("write: %w", err)
		}
	}

	if err := writer.Close(); err != nil {
		return fmt.Errorf("close: %w", err)
	}

	if _, err := msg.Write(buf.Bytes()); err != nil {
		return fmt.Errorf("write: %w", err)
	}

	input := &ses.SendRawEmailInput{
		RawMessage: &types.RawMessage{
			Data: msg.Bytes(),
		},
	}

	_, err := m.svc.SendRawEmail(ctx, input)
	return err
}
