package db

type config struct {
	DataSourceName string
	DriverName     string
}

func (c *config) options(opts ...Option) {
	for _, opt := range opts {
		opt(c)
	}
}

type Option func(*config)

func WithDataSourceName(name string) Option {
	return func(c *config) {
		c.DataSourceName = name
	}
}

func WithDriverName(name string) Option {
	return func(c *config) {
		c.DriverName = name
	}
}
