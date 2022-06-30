package model

import (
	"errors"
	"log"
)

var (
	ErrDeviceNotFound       = errors.New("device not found")
	ErrDeviceAlreadyAdopted = errors.New("device already adopted")
)

func (d *Devices) Init() {
	d.Pending = make(map[string]InterfacePendingDevice)
	d.Adopted = make(map[string]InterfaceAdoptedDevice)
}

func (d *Devices) Adopt(mac string) error {
	if _, ok := d.Pending[mac]; !ok {
		return ErrDeviceNotFound
	}

	if _, ok := d.Adopted[mac]; ok {
		return ErrDeviceAlreadyAdopted
	}

	adopted, err := d.Pending[mac].Adopt()
	if err != nil {
		return err
	}
	d.Adopted[mac] = adopted
	delete(d.Pending, mac)
	return nil
}

func (d *Devices) Delete(mac string) error {
	if _, ok := d.Adopted[mac]; !ok {
		return ErrDeviceNotFound
	}
	if err := d.Adopted[mac].Delete(); err != nil {
		return err
	}
	delete(d.Adopted, mac)
	return nil
}

type Devices struct {
	Adopted adoptedMap `jsonapi:"attr,adopted,omitempty"`
	Pending pendingMap `jsonapi:"attr,pending,omitempty"`
}

type InterfacePendingDevice interface {
	Refresh()
	IsExpired() bool
	GetMac() string
	Adopt() (InterfaceAdoptedDevice, error)
}

type InterfaceAdoptedDevice interface {
	Delete() error
}

type adoptedMap map[string]InterfaceAdoptedDevice

type pendingMap map[string]InterfacePendingDevice

func (p pendingMap) Save(device InterfacePendingDevice) {
	device.Refresh()
	if _, ok := p[device.GetMac()]; !ok {
		log.Printf("New adoption request from %v", device.GetMac())
		p[device.GetMac()] = device
	}
}
