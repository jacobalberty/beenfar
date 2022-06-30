package util

// An InformResponse is a response to an Inform request.
//
// swagger:response informResponse
type InformResponse struct {
	Type          string `json:"_type"`
	Interval      int64  `json:"interval"`
	ServerTimeUTC int64  `json:"server_time_in_utc"`
}
