package internal

func routes(router interface {
	Get(string, ...any)
	Post(string, ...any)
}) {
	router.Get("/api/v1/orders", nil)
	router.Post("/api/v1/orders", nil)
}

const createdSubject = "orders.created.v1"
