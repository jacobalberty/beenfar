package unifi

// An InformHeartbeatResponse is a heartbeat response to an Inform request.
//
// swagger:response informResponse
type InformHeartbeatResponse struct {
	// value "noop"
	Type          string `json:"_type"`
	Interval      int64  `json:"interval"`
	ServerTimeUTC int64  `json:"server_time_in_utc"`
}

// An InformUpgradeResponse is an upgrade command
type InformUpgradeResponse struct {
	// value "upgrade"
	Type string `json:"_type"`
	URL  string `json:"url"`
	// rfc3339 formatted date, server time
	DateTime      string `json:"datetime"`
	ServerTimeUTC int64  `json:"server_time_in_utc"`
	// Firmware version
	Version string `json:"version"`
	// Server time as unix timestamp
	Time int64 `json:"time"`
	// unknown id string (5232701de4b0457a2f2f031f)
	ID string `json:"_id"`
	// device ID from database
	DeviceID string `json:"device_id"`
}

// InformConfigUpdateResponse is a config update command
type InformConfigUpdateResponse struct {
	// value "setparam"
	Type             string `json:"_type"`
	PortConfig       string `json:"port_cfg"`
	AnalogConfig     string `json:"analog_cfg"`
	AuthorizedGuests string `json:"authorized_guests"`
	BlockedStations  string `json:"blocked_stations"`
	ConfigVersion    string `json:"cfg_version"`
	ManagementConfig string `json:"mgmt_cfg"`
	SystemConfig     string `json:"system_cfg"`
	ServerTimeUTC    int64  `json:"server_time_in_utc"`
}

// InformRebootResponse is a reboot command
type InformRebootResponse struct {
	// value "reboot"
	Type string `json:"_type"`
	// device ID from database
	DeviceID      string `json:"device_id"`
	ServerTimeUTC int64  `json:"server_time_in_utc"`
	// server time as unix timestamp
	Time int64 `json:"time"`
	// unknown id string (5232701de4b0457a2f2f031f)
	ID string `json:"_id"`
}

// InformLocateResponse is a locate command
type InformLocateResponse struct {
	// value "cmd"
	Type string `json:"_type"`
	// value "locate"
	Command string `json:"cmd"`
	// rfc3339 formatted date, server time
	DateTime string `json:"datetime"`
	// device ID from database
	DeviceID      string `json:"device_id"`
	ServerTimeUTC int64  `json:"server_time_in_utc"`
	// server time as unix timestamp
	Time int64 `json:"time"`
	// unknown id string (5232701de4b0457a2f2f031f)
	ID string `json:"_id"`
}

// InformCommandResponse is a command response
type InformCommandResponse struct {
	// value "cmd"
	Type  string `json:"_type"`
	Admin struct {
		// database id of admin
		ID string `json:"_id"`
		// admin language (en_US)
		Lang     string `json:"lang"`
		Name     string `json:"name"`
		Password string `json:"x_password"`
	} `json:"_admin"`
	// unknown id string (5232701de4b0457a2f2f031f)
	ID            string `json:"_id"`
	DateTime      string `json:"datetime"`
	ServerTimeUTC int64  `json:"server_time_in_utc"`
	Time          int64  `json:"time"`
	// device ID from database
	DeviceID   string `json:"device_id"`
	Command    string `json:"cmd"`
	MAC        string `json:"mac"`
	Model      string `json:"model"`
	Parameters string `json:"-"`
}
