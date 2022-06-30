package controller

import (
	"crypto/rand"
	"io/ioutil"
	"log"
	"net/http"

	"github.com/jacobalberty/beenfar/service/model"
	"github.com/jacobalberty/beenfar/util"
	"github.com/julienschmidt/httprouter"
)

type unifiHandler struct {
	key     []byte
	devices *model.Devices
}

func (h *unifiHandler) Init(router *httprouter.Router, devices *model.Devices) {
	if len(h.key) != 16 {
		h.key = make([]byte, 16)
		n, err := rand.Read(h.key)
		if n != 16 || err != nil {
			log.Fatal("error generating key")
		}
		log.Printf("Generated new key: %x", h.key)
	}

	h.devices = devices

	// UniFi specific api
	router.POST("/inform", h.postInformHandler)

}

// postInformHandler swagger:route POST /inform unifi postInform
//
// Handles communication between the controller and UniFi equipment.
//
// Responses:
//   200: informResponse
//   404: description:Returned to equipment that has not been adopted yet.
func (h *unifiHandler) postInformHandler(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
	bodyBuffer, _ := ioutil.ReadAll(r.Body)

	ipd, err := util.NewInformPD(bodyBuffer)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
	if _, ok := h.devices.Adopted[ipd.Mac]; ok {
		// Adopted
		ipd.Key = h.key
	} else {
		// Pending adoption
		h.devices.Pending.Save(ipd.Mac)
		http.Error(w, "", http.StatusNotFound)
	}
}
