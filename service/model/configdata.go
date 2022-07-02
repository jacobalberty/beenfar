package model

type ConfigData struct {
	WifiNetworks map[string]WifiNetworkConfig `json:"wifi_networks"`
}

func NewConfigData() *ConfigData {
	return &ConfigData{
		WifiNetworks: make(map[string]WifiNetworkConfig),
	}
}
