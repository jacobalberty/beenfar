package model

import (
	"time"

	"github.com/jacobalberty/beenfar/util"
)

func UnifiPendingDeviceFromInformPD(informPD *util.InformPD) UnifiPendingDevice {
	return UnifiPendingDevice{
		Mac:       informPD.Mac,
		Timestamp: time.Now().Unix(),
		InformPD:  informPD,
	}
}

// Implements models.InterfacePendingDevice
type UnifiPendingDevice struct {
	Timestamp int64          `json:"last_seen"`
	Mac       string         `json:"mac"`
	InformPD  *util.InformPD `json:"-"`
}

func (d *UnifiPendingDevice) Init(informPD *util.InformPD) {
	d.Mac = informPD.Mac
	d.InformPD = informPD
}

func (d *UnifiPendingDevice) Refresh() {
	d.Timestamp = time.Now().Unix()
}

func (d UnifiPendingDevice) GetTimestamp() int64 {
	return d.Timestamp
}

func (d UnifiPendingDevice) GetMac() string {
	return d.Mac
}

func (d UnifiPendingDevice) IsExpired() bool {
	return time.Now().Unix()-d.Timestamp > 60
}

func (d UnifiPendingDevice) Adopt() (InterfaceAdoptedDevice, error) {
	adopted := UnifiAdoptedDevice{
		Mac:       d.Mac,
		Timestamp: time.Now().Unix(),
		InformPD:  d.InformPD,
	}
	return &adopted, nil
}

type UnifiAdoptedDevice struct {
	Timestamp int64          `json:"last_seen"`
	Mac       string         `json:"mac"`
	InformPD  *util.InformPD `json:"-"`
}

// Return MAC address of the device
func (d UnifiAdoptedDevice) GetMac() string {
	return d.Mac
}

func (d *UnifiAdoptedDevice) Delete() error {
	return nil
}
