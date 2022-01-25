// Copyright © 2021 Luther Systems, Ltd. All right reserved.

package mailer

import (
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
func (m *SES) Send(content string, email string, subject string) error {
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
	_, err := m.svc.SendEmail(input)
	if err != nil {
		return err
	}
	return nil
}
