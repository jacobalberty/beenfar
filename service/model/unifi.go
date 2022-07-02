package model

import (
	"time"

	"github.com/jacobalberty/beenfar/util"
)

func UnifiPendingDeviceFromInformPD(informPD *util.InformPD) Device {
	return Device{
		Mac:       informPD.Mac,
		Timestamp: time.Now().Unix(),
		InformPD:  informPD,
	}
}

type Device struct {
	Timestamp int64          `json:"timestamp"`
	Mac       string         `json:"mac"`
	InformPD  *util.InformPD `json:"-"`
}

func (d *Device) Init(informPD *util.InformPD) {
	d.Mac = informPD.Mac
	d.InformPD = informPD
}

func (d *Device) Refresh() {
	d.Timestamp = time.Now().Unix()
}

func (d Device) GetTimestamp() int64 {
	return d.Timestamp
}

func (d Device) GetMac() string {
	return d.Mac
}

func (d Device) IsExpired() bool {
	return time.Now().Unix()-d.Timestamp > 60
}

func (d Device) Adopt() (Device, error) {
	return d, nil
}

func (d Device) Delete() error {
	return nil
}
