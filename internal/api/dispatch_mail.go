package api

import (
	"context"

	"github.com/gastownhall/gascity/internal/mail"
)

type socketMailListPayload struct {
	Agent  string `json:"agent,omitempty"`
	Status string `json:"status,omitempty"`
	Rig    string `json:"rig,omitempty"`
	Limit  *int   `json:"limit,omitempty"`
	Cursor string `json:"cursor,omitempty"`
}

type socketMailGetPayload struct {
	ID  string `json:"id"`
	Rig string `json:"rig,omitempty"`
}

type socketMailCountPayload struct {
	Agent string `json:"agent,omitempty"`
	Rig   string `json:"rig,omitempty"`
}

type socketMailThreadPayload struct {
	ID  string `json:"id"`
	Rig string `json:"rig,omitempty"`
}

type socketMailReplyPayload struct {
	ID      string `json:"id"`
	Rig     string `json:"rig,omitempty"`
	From    string `json:"from"`
	Subject string `json:"subject"`
	Body    string `json:"body"`
}

func init() {
	RegisterAction("mail.list", ActionDef{
		Description:       "List mail messages",
		RequiresCityScope: true,
		SupportsWatch:     true,
	}, func(_ context.Context, s *Server, payload socketMailListPayload) (listResponse, error) {
		items, err := s.Mail.List(payload.Agent, payload.Status, payload.Rig)
		if err != nil {
			return listResponse{}, err
		}
		pp := socketPageParams(payload.Limit, payload.Cursor, 50)
		if !pp.IsPaging {
			total := len(items)
			if pp.Limit < len(items) {
				items = items[:pp.Limit]
			}
			return listResponse{Items: items, Total: total}, nil
		}
		page, total, nextCursor := paginate(items, pp)
		if page == nil {
			page = []mail.Message{}
		}
		return listResponse{Items: page, Total: total, NextCursor: nextCursor}, nil
	})

	RegisterAction("mail.get", ActionDef{
		Description:       "Get a mail message",
		RequiresCityScope: true,
	}, func(_ context.Context, s *Server, payload socketMailGetPayload) (mail.Message, error) {
		return s.Mail.Get(payload.ID, payload.Rig)
	})

	RegisterAction("mail.count", ActionDef{
		Description:       "Count mail messages",
		RequiresCityScope: true,
	}, func(_ context.Context, s *Server, payload socketMailCountPayload) (map[string]int, error) {
		return s.Mail.Count(payload.Agent, payload.Rig)
	})

	RegisterAction("mail.thread", ActionDef{
		Description:       "Get mail thread",
		RequiresCityScope: true,
	}, func(_ context.Context, s *Server, payload socketMailThreadPayload) (listResponse, error) {
		result, err := s.Mail.Thread(payload.ID, payload.Rig)
		if err != nil {
			return listResponse{}, err
		}
		return listResponse{Items: result, Total: len(result)}, nil
	})

	RegisterAction("mail.read", ActionDef{
		Description:       "Mark mail as read",
		IsMutation:        true,
		RequiresCityScope: true,
	}, func(_ context.Context, s *Server, payload socketMailGetPayload) (map[string]string, error) {
		return s.Mail.Read(payload.ID, payload.Rig)
	})

	RegisterAction("mail.mark_unread", ActionDef{
		Description:       "Mark mail as unread",
		IsMutation:        true,
		RequiresCityScope: true,
	}, func(_ context.Context, s *Server, payload socketMailGetPayload) (map[string]string, error) {
		return s.Mail.MarkUnread(payload.ID, payload.Rig)
	})

	RegisterAction("mail.archive", ActionDef{
		Description:       "Archive a mail message",
		IsMutation:        true,
		RequiresCityScope: true,
	}, func(_ context.Context, s *Server, payload socketMailGetPayload) (map[string]string, error) {
		return s.Mail.Archive(payload.ID, payload.Rig)
	})

	RegisterAction("mail.reply", ActionDef{
		Description:       "Reply to a mail message",
		IsMutation:        true,
		RequiresCityScope: true,
	}, func(_ context.Context, s *Server, payload socketMailReplyPayload) (mail.Message, error) {
		return s.Mail.Reply(payload.ID, payload.Rig, mailReplyRequest{
			From:    payload.From,
			Subject: payload.Subject,
			Body:    payload.Body,
		})
	})

	RegisterAction("mail.send", ActionDef{
		Description:       "Send a mail message",
		IsMutation:        true,
		RequiresCityScope: true,
	}, func(_ context.Context, s *Server, p mailSendRequest) (mail.Message, error) {
		return s.Mail.Send(p)
	})

	RegisterAction("mail.delete", ActionDef{
		Description:       "Delete a mail message",
		IsMutation:        true,
		RequiresCityScope: true,
	}, func(_ context.Context, s *Server, payload socketMailGetPayload) (map[string]string, error) {
		if err := s.Mail.Delete(payload.ID, payload.Rig); err != nil {
			return nil, err
		}
		return map[string]string{"status": "deleted"}, nil
	})
}
