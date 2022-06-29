package util

type InformResponse struct {
	Type          string `json:"_type"`
	Interval      int64
	ServerTimeUTC int64
}
