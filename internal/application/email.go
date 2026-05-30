package application

import "context"

// EmailMessage é a mensagem de e-mail no modelo da aplicação.
type EmailMessage struct {
	To       string
	Subject  string
	HTMLBody string
	TextBody string
}

// EmailSender é a porta de saída para envio de e-mail. A implementação
// concreta (SMTP) vive em infrastructure/external/email.
type EmailSender interface {
	Send(ctx context.Context, msg EmailMessage) error
}
