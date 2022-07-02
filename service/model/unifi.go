package model

import (
	"github.com/jacobalberty/beenfar/util"
)

type UnifiDevice struct {
	informPD *util.InformBuilder `json:"-"`
}

func (ud *UnifiDevice) Init(informPD *util.InformBuilder) {
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
