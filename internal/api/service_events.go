package api

import (
	"github.com/gastownhall/gascity/internal/events"
)

// EventService is the domain interface for event operations.
type EventService interface {
	List(filter events.Filter) ([]events.Event, error)
}

// eventService is the default EventService implementation.
type eventService struct {
	s *Server
}

func (e *eventService) List(filter events.Filter) ([]events.Event, error) {
	return e.s.listEvents(filter)
}
