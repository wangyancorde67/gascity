package api

import "context"

type socketConvoyItemsPayload struct {
	ID    string   `json:"id"`
	Items []string `json:"items"`
}

func init() {
	RegisterVoidAction("convoys.list", ActionDef{
		Description:       "List convoys",
		RequiresCityScope: true,
		SupportsWatch:     true,
	}, func(_ context.Context, s *Server) (listResponse, error) {
		items := s.Convoys.List()
		return listResponse{Items: items, Total: len(items)}, nil
	})

	RegisterAction("convoy.get", ActionDef{
		Description:       "Get convoy details",
		RequiresCityScope: true,
	}, func(_ context.Context, s *Server, payload socketIDPayload) (map[string]any, error) {
		return s.Convoys.Get(payload.ID)
	})

	RegisterAction("convoy.create", ActionDef{
		Description:       "Create a convoy",
		IsMutation:        true,
		RequiresCityScope: true,
	}, func(_ context.Context, s *Server, payload convoyCreateRequest) (any, error) {
		return s.Convoys.Create(payload)
	})

	RegisterAction("convoy.add", ActionDef{
		Description:       "Add items to a convoy",
		IsMutation:        true,
		RequiresCityScope: true,
	}, func(_ context.Context, s *Server, payload socketConvoyItemsPayload) (map[string]string, error) {
		if err := s.Convoys.AddItems(payload.ID, payload.Items); err != nil {
			return nil, err
		}
		return map[string]string{"status": "updated"}, nil
	})

	RegisterAction("convoy.remove", ActionDef{
		Description:       "Remove items from a convoy",
		IsMutation:        true,
		RequiresCityScope: true,
	}, func(_ context.Context, s *Server, payload socketConvoyItemsPayload) (map[string]string, error) {
		if err := s.Convoys.RemoveItems(payload.ID, payload.Items); err != nil {
			return nil, err
		}
		return map[string]string{"status": "updated"}, nil
	})

	RegisterAction("convoy.check", ActionDef{
		Description:       "Check convoy completion",
		RequiresCityScope: true,
	}, func(_ context.Context, s *Server, payload socketIDPayload) (any, error) {
		return s.Convoys.Check(payload.ID)
	})

	RegisterAction("convoy.close", ActionDef{
		Description:       "Close a convoy",
		IsMutation:        true,
		RequiresCityScope: true,
	}, func(_ context.Context, s *Server, payload socketIDPayload) (map[string]string, error) {
		if err := s.Convoys.Close(payload.ID); err != nil {
			return nil, err
		}
		return map[string]string{"status": "closed"}, nil
	})

	RegisterAction("convoy.delete", ActionDef{
		Description:       "Delete a convoy",
		IsMutation:        true,
		RequiresCityScope: true,
	}, func(_ context.Context, s *Server, payload socketIDPayload) (map[string]string, error) {
		if err := s.Convoys.Delete(payload.ID); err != nil {
			return nil, err
		}
		return map[string]string{"status": "deleted"}, nil
	})
}
