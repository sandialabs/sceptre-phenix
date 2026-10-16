package store

var DefaultStore Store = NewBoltDB() //nolint:gochecknoglobals // default implementation

func Init(opts ...Option) error {
	s := NewBoltDB()
	if err := s.Init(opts...); err != nil {
		return err
	}

	DefaultStore = s

	return nil
}

func Close() error {
	return DefaultStore.Close()
}

func List(kinds ...string) (Configs, error) {
	return DefaultStore.List(kinds...)
}

func Get(config *Config) error {
	return DefaultStore.Get(config)
}

func Create(config *Config) error {
	return DefaultStore.Create(config)
}

func Update(config *Config) error {
	return DefaultStore.Update(config)
}

func Delete(config *Config) error {
	return DefaultStore.Delete(config)
}

func IsInitialized(component Component) bool {
	return DefaultStore.IsInitialized(component)
}

func InitializeComponent(component Component) error {
	return DefaultStore.InitializeComponent(component)
}
