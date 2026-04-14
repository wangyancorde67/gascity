package api

// OrderService is the domain interface for order operations.
type OrderService interface {
	Check() map[string]any
	History(scopedName string, limit int, before string) (any, error)
	Feed(scopeKind, scopeRef string, limit int) (any, error)
}

// orderService is the default OrderService implementation.
type orderService struct {
	s *Server
}

func (o *orderService) Check() map[string]any {
	return o.s.checkOrders()
}

func (o *orderService) History(scopedName string, limit int, before string) (any, error) {
	return o.s.getOrderHistory(scopedName, limit, before)
}

func (o *orderService) Feed(scopeKind, scopeRef string, limit int) (any, error) {
	return o.s.getOrdersFeed(scopeKind, scopeRef, limit)
}
