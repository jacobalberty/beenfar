package model

import "time"

func (d *Devices) Init() {
	d.Pending = make(map[string]*pending)
	d.Adopted = make(map[string]*adopted)
}

type Devices struct {
	Adopted map[string]*adopted `jsonapi:"attr,adopted,omitempty"`
	Pending map[string]*pending `jsonapi:"attr,pending,omitempty"`
}

type adopted struct {
	Timestamp int64 `json:"-"`
}

type pending struct {
	Timestamp int64 `json:"-"`
}

type pendingMap map[string]*pending

func (m *pendingMap) Add(key string) {
	(*m)[key] = &pending{Timestamp: time.Now().Unix()}
}
