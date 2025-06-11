// Copyright © 2025 Luther Systems, Ltd. All right reserved.

package mailer

import (
	"bytes"
	"context"
	"fmt"
	"mime/multipart"
	"net/textproto"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ses"
)

const (
	// CharSet is the character set used in all SES emails.
	CharSet = "UTF-8"
)

// SES sends email notifications via AWS SES.
type SES struct {
	sender string
	svc    *ses.SES
}

// NewSES constructs a new mailer that uses AWS SES to send emails.
func NewSES(region string, sender string) (*SES, error) {
	sess, err := session.NewSession(&aws.Config{
		Region: aws.String(region)},
	)
	if err != nil {
		return nil, err
	}
	svc := ses.New(sess)
	return &SES{
		sender: sender,
		svc:    svc,
	}, nil
}

// Send send an email to a person.
func (m *SES) Send(ctx context.Context, content string, email string, subject string) error {
	// Assemble the email.
	input := &ses.SendEmailInput{
		Destination: &ses.Destination{
			CcAddresses: []*string{},
			ToAddresses: []*string{
				aws.String(email),
			},
		},
		Message: &ses.Message{
			Body: &ses.Body{
				Html: &ses.Content{
					Charset: aws.String(CharSet),
					Data:    aws.String(content),
				},
			},
			Subject: &ses.Content{
				Charset: aws.String(CharSet),
				Data:    aws.String(subject),
			},
		},
		Source: aws.String(m.sender),
	}
	// Attempt to send the email.
	_, err := m.svc.SendEmailWithContext(ctx, input)
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
		msg.WriteString(fmt.Sprintf("%s: %s\r\n", k, v))
	}
	msg.WriteString("\r\n")

	// Write HTML body part
	bodyWriter, _ := writer.CreatePart(textproto.MIMEHeader{
		"Content-Type": {"text/html; charset=utf-8"},
	})
	bodyWriter.Write([]byte(body))

	// Attach files
	for _, att := range attachments {
		partHeader := textproto.MIMEHeader{}
		partHeader.Set("Content-Type", "application/zip")
		partHeader.Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, att.Filename))
		part, _ := writer.CreatePart(partHeader)
		part.Write(att.Data)
	}

	writer.Close()
	msg.Write(buf.Bytes())

	input := &ses.SendRawEmailInput{
		RawMessage: &ses.RawMessage{
			Data: msg.Bytes(),
		},
	}

	_, err := m.svc.SendRawEmailWithContext(ctx, input)
	return err
}
