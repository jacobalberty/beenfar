package model

import (
	"log"
)

func (d *Devices) Init() {
	d.Pending = make(map[string]*InterfacePendingDevice)
	d.Adopted = make(map[string]*InterfaceAdoptedDevice)
}

type Devices struct {
	Adopted adoptedMap `jsonapi:"attr,adopted,omitempty"`
	Pending pendingMap `jsonapi:"attr,pending,omitempty"`
}

type InterfacePendingDevice interface {
	Refresh()
	IsExpired() bool
	GetMac() string
}

type InterfaceAdoptedDevice interface{}

type adoptedMap map[string]*InterfaceAdoptedDevice

type pendingMap map[string]*InterfacePendingDevice

func (p pendingMap) Save(device InterfacePendingDevice) {
	device.Refresh()
	if _, ok := p[device.GetMac()]; !ok {
		log.Printf("New adoption request from %v", device.GetMac())
		p[device.GetMac()] = &device
	}
}
