package api

import (
	"github.com/gastownhall/gascity/internal/mail"
)

// MailService is the domain interface for mail operations.
type MailService interface {
	List(agent, status, rig string) ([]mail.Message, error)
	Get(id, rig string) (mail.Message, error)
	Count(agent, rig string) (map[string]int, error)
	Thread(threadID, rig string) ([]mail.Message, error)
	Read(id, rig string) (map[string]string, error)
	MarkUnread(id, rig string) (map[string]string, error)
	Archive(id, rig string) (map[string]string, error)
	Reply(id, rig string, body mailReplyRequest) (mail.Message, error)
	Send(body mailSendRequest) (mail.Message, error)
	Delete(id, rig string) error
}

// mailService is the default MailService implementation.
type mailService struct {
	s *Server
}

func (m *mailService) List(agent, status, rig string) ([]mail.Message, error) {
	return m.s.listMailMessages(agent, status, rig)
}

func (m *mailService) Get(id, rig string) (mail.Message, error) {
	return m.s.getMailMessage(id, rig)
}

func (m *mailService) Count(agent, rig string) (map[string]int, error) {
	return m.s.mailCount(agent, rig)
}

func (m *mailService) Thread(threadID, rig string) ([]mail.Message, error) {
	return m.s.listMailThread(threadID, rig)
}

func (m *mailService) Read(id, rig string) (map[string]string, error) {
	return m.s.markMailRead(id, rig)
}

func (m *mailService) MarkUnread(id, rig string) (map[string]string, error) {
	return m.s.markMailUnread(id, rig)
}

func (m *mailService) Archive(id, rig string) (map[string]string, error) {
	return m.s.archiveMail(id, rig)
}

func (m *mailService) Reply(id, rig string, body mailReplyRequest) (mail.Message, error) {
	return m.s.replyMail(id, rig, body)
}

func (m *mailService) Send(body mailSendRequest) (mail.Message, error) {
	return m.s.sendMail(body)
}

func (m *mailService) Delete(id, rig string) error {
	return m.s.deleteMail(id, rig)
}
