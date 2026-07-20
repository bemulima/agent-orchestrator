package repository

// HealthChecker reports availability of one infrastructure dependency.
type HealthChecker interface {
	Name() string
	Ping(ctx Context) error
}
