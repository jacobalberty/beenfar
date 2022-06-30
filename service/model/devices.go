package model

import (
	"log"
)

func (d *Devices) Init() {
	d.Pending = make(map[string]*InterfacePendingDevice)
	d.Adopted = make(map[string]*adopted)
}

type Devices struct {
	Adopted map[string]*adopted `jsonapi:"attr,adopted,omitempty"`
	Pending pendingMap          `jsonapi:"attr,pending,omitempty"`
}

type adopted struct {
	Timestamp int64 `json:"-"`
}

type pending struct {
	Timestamp int64 `json:"-"`
}

type InterfacePendingDevice interface {
	Refresh()
	IsExpired() bool
	GetMac() string
}

type pendingMap map[string]*InterfacePendingDevice

func (p pendingMap) Save(device InterfacePendingDevice) {
	device.Refresh()
	if _, ok := p[device.GetMac()]; !ok {
		log.Printf("New adoption request from %v", device.GetMac())
		p[device.GetMac()] = &device
	}
}
