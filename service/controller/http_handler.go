package controller

import (
	"log"
	"net/http"

	"github.com/google/jsonapi"
	"github.com/jacobalberty/beenfar/service/model"
	"github.com/julienschmidt/httprouter"
)

type HttpHandler struct {
	key     []byte
	devices *model.Devices
}

func (h *HttpHandler) RegisterHandlers() {
	h.devices = new(model.Devices)
	h.devices.Init()

	router := httprouter.New()

	unifi := unifiHandler{}
	unifi.Init(router, h.devices)

	// Unstable apis
	router.POST("/api/device/adopt/:mac", h.postDeviceAdopt)
	router.GET("/api/device/list", h.getDeviceList)

	log.Fatal(http.ListenAndServe(":8080", router))
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
