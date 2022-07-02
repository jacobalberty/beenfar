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

}

func (d *Devices) Adopt(mac string) error {
	if !d.Pending.Contains(mac) {
		return ErrDeviceNotFound
	}

	if d.Adopted.Contains(mac) {
		return ErrDeviceAlreadyAdopted
	}

	pending := d.Pending.Get(mac)
	adopted, err := pending.Adopt()
	if err != nil {
		return err
	}

	d.Adopted.Save(adopted)
	d.Pending.Remove(mac)
	return nil
}

func (d *Devices) Delete(mac string) error {
	if !d.Adopted.Contains(mac) {
		return ErrDeviceNotFound
	}

	adopted := d.Adopted.Get(mac)

	if err := adopted.Delete(); err != nil {
		return err
	}
	d.Adopted.Remove(mac)
	return nil
}

type Devices struct {
	Adopted adoptedList `jsonapi:"attr,adopted,omitempty"`
	Pending pendingList `jsonapi:"attr,pending,omitempty"`
}

type InterfacePendingDevice interface {
	Refresh()
	IsExpired() bool
	GetMac() string
	Adopt() (InterfaceAdoptedDevice, error)
}

type InterfaceAdoptedDevice interface {
	GetMac() string
	Delete() error
}

type adoptedList []InterfaceAdoptedDevice

type pendingList []InterfacePendingDevice

// Check if device is already in pending list
func (p pendingList) Contains(mac string) bool {
	for _, d := range p {
		if d.GetMac() == mac {
			return true
		}
	}
	return false
}

// Remove device from pending list
func (p *pendingList) Remove(mac string) {
	pending := *p
	for i, d := range *p {
		if d.GetMac() == mac {
			*p = append(pending[:i], pending[i+1:]...)
			break
		}
	}
}

// Get device from pending list
func (p pendingList) Get(mac string) InterfacePendingDevice {
	for _, d := range p {
		if d.GetMac() == mac {
			return d
		}
	}
	return nil
}

// Get all pending devices
func (p pendingList) GetAll() []InterfacePendingDevice {
	return p
}

// Get all adopted devices
func (a adoptedList) GetAll() []InterfaceAdoptedDevice {
	return a
}

// Get device from adopted list
func (a adoptedList) Get(mac string) InterfaceAdoptedDevice {
	for _, d := range a {
		if d.GetMac() == mac {
			return d
		}
	}
	return nil
}

// Remove device from adopted list
func (a *adoptedList) Remove(mac string) {
	adopted := *a
	for i, d := range *a {
		if d.GetMac() == mac {
			*a = append(adopted[:i], adopted[i+1:]...)
			break
		}
	}
}

// Check if device is already in adopted list
func (a adoptedList) Contains(mac string) bool {
	for _, d := range a {
		if d.GetMac() == mac {
			return true
		}
	}
	return false
}

// Save device to adopted list
func (a *adoptedList) Save(device InterfaceAdoptedDevice) {
	found := false
	for _, d := range *a {
		if d.GetMac() == device.GetMac() {
			found = true
			break
		}
	}

	if !found {
		*a = append(*a, device)
	}
}

// Save device to pending list
func (p *pendingList) Save(device InterfacePendingDevice) {
	device.Refresh()
	found := false
	for _, d := range *p {
		if d.GetMac() == device.GetMac() {
			found = true
			break
		}
	}

	if !found {
		log.Printf("New adoption request from %v", device.GetMac())
		*p = append(*p, device)
	}
}
