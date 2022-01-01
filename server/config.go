package server

type Config struct {
	Yunboss       string
	Apiport       string
	Port          string
	Websocket     string
	Ip            string
	LogPath       string `mapstructure:"log_path"`
	Protocol      string
	Logsize       int
	Clientmaxsize int
}
