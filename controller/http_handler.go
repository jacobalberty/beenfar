package controller

import (
	"io/ioutil"
	"log"
	"net/http"
	"time"

	"github.com/google/jsonapi"
	"github.com/jacobalberty/beenfar/util"
	"github.com/julienschmidt/httprouter"
)

type HttpHandler struct {
	key     []byte
	devices devices
}

type devices struct {
	Adopted map[string]*adopted `jsonapi:"attr,adopted,omitempty"`
	Pending map[string]*pending `jsonapi:"attr,pending,omitempty"`
}

type adopted struct {
	Timestamp int64 `json:"-"`
}

type pending struct {
	Timestamp int64 `json:"-"`
}

func (h *HttpHandler) RegisterHandlers() {
	h.devices.Pending = make(map[string]*pending)
	h.devices.Adopted = make(map[string]*adopted)
	router := httprouter.New()

	// UniFi specific api
	router.POST("/inform", h.postInformHandler)

	// Unstable apis
	router.POST("/api/device/adopt/:mac", h.postDeviceAdopt)
	router.GET("/api/device/list", h.getDeviceList)

	log.Fatal(http.ListenAndServe(":8080", router))
}

func (h *HttpHandler) postInformHandler(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
	bodyBuffer, _ := ioutil.ReadAll(r.Body)

	ipd, err := util.NewInformPD(bodyBuffer)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
	if _, ok := h.devices.Adopted[ipd.Mac]; ok {
		// Adopted
	} else {
		// Pending adoption
		if _, ok := h.devices.Pending[ipd.Mac]; !ok {
			log.Printf("New adoption request from %v", ipd.Mac)
			h.devices.Pending[ipd.Mac] = &pending{time.Now().Unix()}
		} else {
			h.devices.Pending[ipd.Mac].Timestamp = time.Now().Unix()
		}
	}
	response, err := ipd.BuildResponse(util.InformResponse{
		Type:          "noop",
		Interval:      300, // 5 minutes
		ServerTimeUTC: time.Now().Unix(),
	})

	w.Write(response)
}

func (h *HttpHandler) getDeviceList(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
	w.Header().Set("Content-Type", jsonapi.MediaType)
	w.WriteHeader(http.StatusOK)
	if err := jsonapi.MarshalPayload(w, &h.devices); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}

}

func (h *HttpHandler) postDeviceAdopt(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
	w.Header().Set("Content-Type", jsonapi.MediaType)
	w.WriteHeader(http.StatusNotImplemented)
	if err := jsonapi.MarshalErrors(w, []*jsonapi.ErrorObject{{
		Title:  "Device Adoption",
		Detail: "Device adoption is not yet implemented",
		Status: "501",
	}}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
