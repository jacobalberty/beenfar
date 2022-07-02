package model

import (
	"github.com/jacobalberty/beenfar/service/adapter/unifi"
)

type UnifiDevice struct {
	informPD *unifi.InformBuilder `json:"-"`
}

func (ud *UnifiDevice) Init(informPD *unifi.InformBuilder) {
	ud.informPD = informPD
}

func (ud UnifiDevice) GetMac() string {
	return ud.informPD.GetMac()
}

func (ud UnifiDevice) Adopt() error {
	return nil
}

func (ud UnifiDevice) Delete() error {
	return nil
}

func (ud UnifiDevice) Refresh() {

}
