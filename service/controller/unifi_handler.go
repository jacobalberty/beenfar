package controller

import (
	"crypto/rand"
	"io/ioutil"
	"log"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jacobalberty/beenfar/service/model"
	"github.com/jacobalberty/beenfar/util"
)

type unifiHandler struct {
	key     []byte
	devices *model.Devices
}

func (h *unifiHandler) Init(router *chi.Mux, devices *model.Devices) {
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
	router.Post("/inform", h.postInformHandler)

}

// postInformHandler swagger:route POST /inform unifi postInform
//
// Handles communication between the controller and UniFi equipment.
//
// Responses:
//   200: informResponse
//   404: description:Returned to equipment that has not been adopted yet.
func (h *unifiHandler) postInformHandler(w http.ResponseWriter, r *http.Request) {
	bodyBuffer, _ := ioutil.ReadAll(r.Body)

	ipd, err := util.NewInformPD(bodyBuffer)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
	if h.devices.Adopted.Contains(ipd.Mac) {
		// Adopted
		ipd.Key = h.key
	} else {
		// Pending adoption
		pd := model.UnifiDevice{}
		pd.Init(ipd)
		d := model.Device{}
		d.Init(pd)
		h.devices.Pending.Save(d)
		http.Error(w, "", http.StatusNotFound)
	}
}
