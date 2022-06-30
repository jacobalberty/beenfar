package model

import (
	"log"
	"time"
)

func (d *Devices) Init() {
	d.Pending = make(map[string]*pending)
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

type pendingMap map[string]*pending

func (p pendingMap) Save(mac string) {
	if _, ok := p[mac]; !ok {
		log.Printf("New adoption request from %v", mac)
		p[mac] = &pending{Timestamp: time.Now().Unix()}
	} else {
		p[mac].Timestamp = time.Now().Unix()
	}

}
